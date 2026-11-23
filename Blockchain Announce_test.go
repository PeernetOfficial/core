/*
File Name:  Blockchain Announce_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"bytes"
	"crypto/ed25519"
	"sort"
	"testing"
	"time"

	"github.com/PeernetOfficial/core/blockchain"
	"github.com/PeernetOfficial/core/dht"
	"github.com/PeernetOfficial/core/protocol"
)

// testAnnounceOut is an Announcement sent in a test.
type testAnnounceOut struct {
	receiver ed25519.PublicKey
	requests *protocol.Requests
}

// testAnnounceBackend returns a backend with a blockchain of the given height and the count of peers in the peer list and the routing table.
// Each Announcement that is sent is appended to out.
func testAnnounceBackend(t *testing.T, height, peers int) (backend *Backend, out *[]testAnnounceOut) {
	backend, network := testBackendLoopback(t)
	backend.UserBlockchain = testBlockchain(t, backend.peerPrivateKey, height)

	for n := 0; n < peers; n++ {
		peer, _, _ := testPeerSocket(t, backend, network)
		backend.nodesDHT.AddNode(&dht.Node{ID: peer.PublicKey, Info: peer})
	}
	if count := backend.nodesDHT.NumNodes(); count != peers {
		t.Fatalf("the routing table holds %d peers, expected %d", count, peers)
	}

	out = &[]testAnnounceOut{}
	backend.Filters.MessageOutAnnouncement = func(receiverPublicKey ed25519.PublicKey, peer *PeerInfo, packet *protocol.PacketRaw, requests *protocol.Requests) {
		*out = append(*out, testAnnounceOut{receiverPublicKey, requests})
	}

	return backend, out
}

// TestAnnounceBlockchain checks the announce of the own blockchain: one B_STORE_BLOCKCHAIN request with a valid blockchain record of the own blockchain
// to each of the 20 closest peers to the own Peer ID in the routing table, to all peers if there are fewer. A blockchain of height 0 is not announced.
func TestAnnounceBlockchain(t *testing.T) {
	for _, test := range []struct {
		height, peers, expected int
	}{
		{0, 25, 0},
		{1, 0, 0},
		{1, 3, 3},
		{1, 20, 20},
		{3, 25, 20},
	} {
		backend, out := testAnnounceBackend(t, test.height, test.peers)
		self := backend.peerPublicKey

		if sent := backend.announceBlockchain(0, uint64(test.height)); sent != test.expected || len(*out) != test.expected {
			t.Fatalf("height %d, %d peers: %d sent and %d Announcements, expected %d", test.height, test.peers, sent, len(*out), test.expected)
		}

		// The receivers are the closest peers by XOR distance to the own Peer ID, each once.
		nodes := backend.nodesDHT.Nodes()
		sort.Slice(nodes, func(i, j int) bool {
			for n := range self {
				if a, b := nodes[i].ID[n]^self[n], nodes[j].ID[n]^self[n]; a != b {
					return a < b
				}
			}
			return false
		})

		now := time.Now()

		for n, message := range *out {
			if !bytes.Equal(message.receiver, nodes[n].ID) {
				t.Fatalf("height %d, %d peers: Announcement %d is not sent to the peer of rank %d", test.height, test.peers, n, n)
			}

			requests := message.requests
			if requests == nil || len(requests.StoreBlockchain) != 1 || requests.FindSelf || len(requests.FindPeer) != 0 || len(requests.FindBlockchain) != 0 {
				t.Fatalf("height %d, %d peers: unexpected requests in Announcement %d: %+v", test.height, test.peers, n, requests)
			}

			record := requests.StoreBlockchain[0]
			if !record.PublicKey.Equal(self) || record.Version != 0 || record.Height != uint64(test.height) {
				t.Fatalf("height %d, %d peers: blockchain record of version %d, height %d", test.height, test.peers, record.Version, record.Height)
			}
			if !protocol.BlockchainRecordValid(self, self, record.Version, record.Height, record.Time, record.Signature, now) {
				t.Fatalf("height %d, %d peers: blockchain record is not valid", test.height, test.peers)
			}
			if age := now.Unix() - int64(record.Time); age < 0 || age > 60 {
				t.Fatalf("height %d, %d peers: blockchain record is %d seconds old", test.height, test.peers, age)
			}
		}
	}
}

// TestAnnounceBlockchainDue checks when the own blockchain is announced: the first time once it has a block and a peer to send to, then every 12 hours,
// and after a change of its version or height once the last announce is 10 minutes ago.
func TestAnnounceBlockchainDue(t *testing.T) {
	backend, out := testAnnounceBackend(t, 0, 2)
	start := time.Now()

	var last blockchainAnnounced

	// step checks the count of Announcements sent up to now and the last announce. An expectedLast below 0 means that there was no announce yet.
	step := func(name string, after time.Duration, expectedAnnouncements int, expectedLast time.Duration, expectedVersion, expectedHeight uint64) {
		t.Helper()

		last = backend.announceBlockchainDue(last, start.Add(after))

		if len(*out) != expectedAnnouncements {
			t.Fatalf("%s: %d Announcements sent, expected %d", name, len(*out), expectedAnnouncements)
		}
		if expectedLast < 0 && !last.time.IsZero() || expectedLast >= 0 && !last.time.Equal(start.Add(expectedLast)) {
			t.Fatalf("%s: unexpected time of the last announce", name)
		}
		if last.version != expectedVersion || last.height != expectedHeight {
			t.Fatalf("%s: last announce of version %d, height %d, expected version %d, height %d", name, last.version, last.height, expectedVersion, expectedHeight)
		}
		if expectedAnnouncements > 0 {
			if record := (*out)[len(*out)-1].requests.StoreBlockchain[0]; record.Version != expectedVersion || record.Height != expectedHeight {
				t.Fatalf("%s: blockchain record of version %d, height %d, expected version %d, height %d", name, record.Version, record.Height, expectedVersion, expectedHeight)
			}
		}
	}

	step("height 0", 0, 0, -1, 0, 0)
	step("height 0, a minute later", time.Minute, 0, -1, 0, 0)

	backend.UserBlockchain = testBlockchain(t, backend.peerPrivateKey, 1)

	first := 2 * time.Minute
	step("first block", first, 2, first, 0, 1)
	step("a minute after the announce", first+time.Minute, 2, first, 0, 1)
	step("a second before 12 hours after the announce", first+12*time.Hour-time.Second, 2, first, 0, 1)
	step("12 hours after the announce", first+12*time.Hour, 4, first+12*time.Hour, 0, 1)
	step("a minute after the second announce", first+12*time.Hour+time.Minute, 4, first+12*time.Hour, 0, 1)
	step("24 hours after the first announce", first+24*time.Hour, 6, first+24*time.Hour, 0, 1)

	// A block written less than 10 minutes after an announce is announced 10 minutes after it. Blocks written in between are announced together.
	third := first + 24*time.Hour
	testBlockchainAppend(t, backend, 1)
	step("block written, a minute after the announce", third+time.Minute, 6, third, 0, 1)
	testBlockchainAppend(t, backend, 2)
	step("blocks written, a second before 10 minutes after the announce", third+10*time.Minute-time.Second, 6, third, 0, 1)
	step("blocks written, 10 minutes after the announce", third+10*time.Minute, 8, third+10*time.Minute, 0, 4)
	step("a minute after the announce of the blocks", third+11*time.Minute, 8, third+10*time.Minute, 0, 4)

	// A block written 10 minutes or more after an announce is announced at the next check.
	fourth := third + 10*time.Minute
	step("an hour after the announce of the blocks", fourth+time.Hour, 8, fourth, 0, 4)
	testBlockchainAppend(t, backend, 1)
	step("block written an hour after the announce", fourth+time.Hour+time.Minute, 10, fourth+time.Hour+time.Minute, 0, 5)

	// The 12 hours count from the last announce.
	fifth := fourth + time.Hour + time.Minute
	step("12 hours after the announce before the last", fourth+12*time.Hour, 10, fifth, 0, 5)
	step("a second before 12 hours after the last announce", fifth+12*time.Hour-time.Second, 10, fifth, 0, 5)
	step("12 hours after the last announce", fifth+12*time.Hour, 12, fifth+12*time.Hour, 0, 5)

	// A change of the version alone is announced: a profile field is deleted, which removes its block, and another one is written.
	sixth := fifth + 12*time.Hour
	if _, _, status := backend.UserBlockchain.ProfileWrite([]blockchain.BlockRecordProfile{{Type: 100, Data: []byte{1}}}); status != blockchain.StatusOK {
		t.Fatalf("writing profile field: status %d", status)
	}
	step("profile field written", sixth+time.Hour, 14, sixth+time.Hour, 0, 6)

	if _, _, status := backend.UserBlockchain.ProfileDelete([]uint16{100}); status != blockchain.StatusOK {
		t.Fatalf("deleting profile field: status %d", status)
	}
	if _, _, status := backend.UserBlockchain.ProfileWrite([]blockchain.BlockRecordProfile{{Type: 101, Data: []byte{1}}}); status != blockchain.StatusOK {
		t.Fatalf("writing profile field: status %d", status)
	}
	step("version changed, a minute after the announce", sixth+time.Hour+time.Minute, 14, sixth+time.Hour, 0, 6)
	step("version changed, 10 minutes after the announce", sixth+time.Hour+10*time.Minute, 16, sixth+time.Hour+10*time.Minute, 1, 6)

	// Without a peer in the routing table nothing is sent, and the announce stays due.
	backend, out = testAnnounceBackend(t, 1, 0)
	last = blockchainAnnounced{}

	step("no peer", 0, 0, -1, 0, 0)
}

// TestAnnounceBlockchainListed checks that the peer that receives the announce reports the announcing peer as storing peer of its blockchain.
func TestAnnounceBlockchainListed(t *testing.T) {
	sender, out := testAnnounceBackend(t, 2, 1)

	if sent := sender.announceBlockchain(0, 2); sent != 1 || len(*out) != 1 {
		t.Fatalf("%d sent and %d Announcements, expected 1", sent, len(*out))
	}

	// The receiver knows the announcing peer by a public address.
	receiver := testListingBackend(t, 0)
	peer := testPeerConnections(t, receiver, "203.0.113.1")
	peer.PublicKey = sender.peerPublicKey

	var records []protocol.ResponseRecord
	responses := 0
	receiver.Filters.MessageOutResponse = func(peer *PeerInfo, packet *protocol.PacketRaw, recordsOut []protocol.ResponseRecord) {
		records = recordsOut
		responses++
	}

	announcement := &protocol.MessageAnnouncement{MessageRaw: &protocol.MessageRaw{}, Requests: *(*out)[0].requests}
	peer.cmdAnouncement(announcement, nil)

	if responses != 0 {
		t.Fatalf("%d Responses to the store request, expected none", responses)
	}

	requester := testPeerConnections(t, receiver)
	find := &protocol.MessageAnnouncement{MessageRaw: &protocol.MessageRaw{}, Features: 1 << protocol.FeatureIPv4Listen}
	find.FindBlockchain = [][]byte{sender.peerPublicKey}
	requester.cmdAnouncement(find, nil)

	if responses != 1 || len(records) != 1 || len(records[0].Storing) != 1 {
		t.Fatalf("%d Responses with %d records, expected 1 record with 1 storing peer", responses, len(records))
	}

	storing := records[0].Storing[0]
	if !storing.PublicKey.Equal(sender.peerPublicKey) || storing.BlockchainVersion != 0 || storing.BlockchainHeight != 2 || len(storing.IPv4) != 1 || storing.IPv4[0].IP.String() != "203.0.113.1" {
		t.Fatalf("unexpected storing peer: version %d, height %d, %d IPv4 addresses", storing.BlockchainVersion, storing.BlockchainHeight, len(storing.IPv4))
	}
	if !protocol.BlockchainRecordValid(storing.PublicKey, sender.peerPublicKey, storing.BlockchainVersion, storing.BlockchainHeight, storing.Time, storing.Signature, time.Now()) {
		t.Fatal("blockchain record of the storing peer is not valid")
	}
}
