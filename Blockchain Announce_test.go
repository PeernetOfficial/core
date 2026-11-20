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

		if sent := backend.announceBlockchain(); sent != test.expected || len(*out) != test.expected {
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

// TestAnnounceBlockchainDue checks when the own blockchain is announced: the first time once it has a block and a peer to send to, then every 12 hours.
func TestAnnounceBlockchainDue(t *testing.T) {
	backend, out := testAnnounceBackend(t, 0, 2)
	start := time.Now()

	var last time.Time

	step := func(name string, after time.Duration, expectedAnnouncements int, expectedLast time.Duration) {
		t.Helper()

		last = backend.announceBlockchainDue(last, start.Add(after))

		if len(*out) != expectedAnnouncements {
			t.Fatalf("%s: %d Announcements sent, expected %d", name, len(*out), expectedAnnouncements)
		}
		if expectedLast < 0 && !last.IsZero() || expectedLast >= 0 && !last.Equal(start.Add(expectedLast)) {
			t.Fatalf("%s: unexpected time of the last announce", name)
		}
	}

	step("height 0", 0, 0, -1)
	step("height 0, a minute later", time.Minute, 0, -1)

	backend.UserBlockchain = testBlockchain(t, backend.peerPrivateKey, 1)

	step("first block", 2*time.Minute, 2, 2*time.Minute)
	step("a minute after the announce", 3*time.Minute, 2, 2*time.Minute)
	step("a second before 12 hours after the announce", 12*time.Hour+2*time.Minute-time.Second, 2, 2*time.Minute)
	step("12 hours after the announce", 12*time.Hour+2*time.Minute, 4, 12*time.Hour+2*time.Minute)
	step("a minute after the second announce", 12*time.Hour+3*time.Minute, 4, 12*time.Hour+2*time.Minute)
	step("24 hours after the first announce", 24*time.Hour+2*time.Minute, 6, 24*time.Hour+2*time.Minute)

	// Without a peer in the routing table nothing is sent, and the announce stays due.
	backend, out = testAnnounceBackend(t, 1, 0)
	last = time.Time{}

	step("no peer", 0, 0, -1)
}

// TestAnnounceBlockchainListed checks that the peer that receives the announce reports the announcing peer as storing peer of its blockchain.
func TestAnnounceBlockchainListed(t *testing.T) {
	sender, out := testAnnounceBackend(t, 2, 1)

	if sent := sender.announceBlockchain(); sent != 1 || len(*out) != 1 {
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
