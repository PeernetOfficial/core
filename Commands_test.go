/*
File Name:  Commands_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"

	"github.com/PeernetOfficial/core/blockchain"
	"github.com/PeernetOfficial/core/dht"
	"github.com/PeernetOfficial/core/protocol"
	"github.com/PeernetOfficial/core/store"
)

// testPeerConnections returns a peer with active connections to the IPs. The first one is the latest connection.
func testPeerConnections(t *testing.T, backend *Backend, ips ...string) (peer *PeerInfo) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	peer = &PeerInfo{Backend: backend, PublicKey: publicKey, Features: 1<<protocol.FeatureIPv4Listen | 1<<protocol.FeatureIPv6Listen}
	for _, ip := range ips {
		peer.connectionActive = append(peer.connectionActive, &Connection{Address: &net.UDPAddr{IP: net.ParseIP(ip), Port: 112}, PortInternal: 112})
	}
	if len(peer.connectionActive) > 0 {
		peer.connectionLatest = peer.connectionActive[0]
	}

	return peer
}

// TestAnnouncementResponseAddresses checks the addresses in the peer records of the response to an announcement:
// up to 3 per family in A_RESPONSE_SELF and A_RESPONSE_PEER, at most one per family in B_RESPONSE_BLOCKCHAIN and B_RESPONSE_FILE.
func TestAnnouncementResponseAddresses(t *testing.T) {
	self, _, _ := ed25519.GenerateKey(rand.Reader)

	backend := &Backend{peerPublicKey: self, UserBlockchain: &blockchain.Blockchain{}, dhtStore: store.NewMemoryStore(), networks: &Networks{}}
	backend.initFilters()
	backend.nodesDHT = dht.NewDHT(&dht.Node{ID: self}, 256, bucketSize, alpha)

	peerMany := testPeerConnections(t, backend, "203.0.113.1", "203.0.113.2", "203.0.113.3", "203.0.113.4", "2001:db8::1", "2001:db8::2", "2001:db8::3", "2001:db8::4", "192.168.1.1")
	peerOne := testPeerConnections(t, backend, "203.0.113.5", "2001:db8::5")
	peerLocal := testPeerConnections(t, backend, "192.168.1.7")

	for _, peer := range []*PeerInfo{peerMany, peerOne, peerLocal} {
		backend.nodesDHT.AddNode(&dht.Node{ID: peer.PublicKey, Info: peer})
	}

	fileData := make([]byte, protocol.EmbeddedFileSizeMax+1) // stored, but too big to embed
	fileHash := protocol.HashData(fileData)
	if err := backend.dhtStore.Set(fileHash, fileData); err != nil {
		t.Fatal(err)
	}
	fileOther := protocol.HashData([]byte("not stored"))

	// The response is captured before it is sent. The requester has no connection, sending it fails.
	var records []protocol.ResponseRecord
	backend.Filters.MessageOutResponse = func(peer *PeerInfo, packet *protocol.PacketRaw, recordsOut []protocol.ResponseRecord) {
		records = recordsOut
	}

	requester := testPeerConnections(t, backend)

	announcement := &protocol.MessageAnnouncement{MessageRaw: &protocol.MessageRaw{}, Features: 1<<protocol.FeatureIPv4Listen | 1<<protocol.FeatureIPv6Listen}
	announcement.FindSelf = true
	announcement.FindPeer = [][]byte{peerOne.PublicKey}
	announcement.FindBlockchain = [][]byte{self, peerOne.PublicKey}
	announcement.FindFile = [][]byte{fileHash, fileOther}
	announcement.FindFileEmbed = [][]byte{fileHash}

	requester.cmdAnouncement(announcement, nil)

	expected := []struct {
		recordType   uint8
		addressesMax int
		storing      bool
	}{
		{protocol.ResponseASelf, 3, false},
		{protocol.ResponseAPeer, 3, false},
		{protocol.ResponseBBlockchain, 1, true},
		{protocol.ResponseBBlockchain, 1, false},
		{protocol.ResponseBFile, 1, true},
		{protocol.ResponseBFile, 1, false},
		{protocol.ResponseBFile, 1, true},
	}

	if len(records) != len(expected) {
		t.Fatalf("%d response records, expected %d", len(records), len(expected))
	}

	for n, record := range records {
		if record.Type != expected[n].recordType || !record.IsLast {
			t.Fatalf("record %d: type %d last %t, expected type %d", n, record.Type, record.IsLast, expected[n].recordType)
		}

		// The peer that stores the data lists itself without an address.
		if expected[n].storing != (len(record.Storing) == 1) {
			t.Fatalf("record %d: %d storing peers", n, len(record.Storing))
		}
		for _, storing := range record.Storing {
			if !storing.PublicKey.Equal(self) || len(storing.IPv4) != 0 || len(storing.IPv6) != 0 {
				t.Errorf("record %d: storing peer with %d IPv4 and %d IPv6 addresses", n, len(storing.IPv4), len(storing.IPv6))
			}
		}

		// The closest peers are the 2 peers with public addresses. Only local addresses are not shared over a connection that is not local.
		if len(record.Closest) != 2 {
			t.Fatalf("record %d: %d closest peers, expected 2", n, len(record.Closest))
		}
		for _, closest := range record.Closest {
			count := 1
			if closest.PublicKey.Equal(peerMany.PublicKey) {
				count = expected[n].addressesMax
			} else if !closest.PublicKey.Equal(peerOne.PublicKey) {
				t.Fatalf("record %d: unexpected closest peer", n)
			}

			if len(closest.IPv4) != count || len(closest.IPv6) != count {
				t.Errorf("record %d: closest peer with %d IPv4 and %d IPv6 addresses, expected %d each", n, len(closest.IPv4), len(closest.IPv6), count)
			}
			for _, address := range append(append([]protocol.PeerRecordAddress{}, closest.IPv4...), closest.IPv6...) {
				if IsIPLocal(address.IP) || address.Port != 112 || address.PortReportedInternal != 112 {
					t.Errorf("record %d: unexpected address %s port %d internal %d", n, address.IP.String(), address.Port, address.PortReportedInternal)
				}
			}
		}
	}

	// The whole response encodes and decodes.
	packets, err := protocol.EncodeResponse(false, records, 0, 0, 0, "")
	if err != nil || len(packets) != 1 {
		t.Fatalf("encoding the response: %d packets (%v)", len(packets), err)
	}
	msg := &protocol.MessageRaw{PacketRaw: protocol.PacketRaw{Command: protocol.CommandResponse, Payload: packets[0]}}
	if decoded, err := protocol.DecodeResponse(msg); err != nil || len(decoded.Records) != len(records) {
		t.Fatalf("decoding the response: %v", err)
	}
}

// TestPeer2Record checks the peer record of a peer by what may be shared.
func TestPeer2Record(t *testing.T) {
	peer := testPeerConnections(t, nil, "203.0.113.1", "192.168.1.1", "203.0.113.2", "10.0.0.1", "172.16.0.1", "2001:db8::1", "fd00::1")

	tests := []struct {
		allowLocal, allowIPv4, allowIPv6 bool
		addressesMax                     int
		ipv4, ipv6                       []string
	}{
		{false, true, true, 3, []string{"203.0.113.1", "203.0.113.2"}, []string{"2001:db8::1"}},
		{false, true, true, 1, []string{"203.0.113.1"}, []string{"2001:db8::1"}},
		{true, true, true, 3, []string{"192.168.1.1", "10.0.0.1", "172.16.0.1"}, []string{"fd00::1", "2001:db8::1"}},
		{true, true, true, 1, []string{"192.168.1.1"}, []string{"fd00::1"}},
		{false, true, false, 3, []string{"203.0.113.1", "203.0.113.2"}, nil},
		{false, false, true, 3, nil, []string{"2001:db8::1"}},
	}

	for _, test := range tests {
		record := peer.peer2Record(test.allowLocal, test.allowIPv4, test.allowIPv6, test.addressesMax)
		if record == nil {
			t.Errorf("allow local %t IPv4 %t IPv6 %t max %d: no peer record", test.allowLocal, test.allowIPv4, test.allowIPv6, test.addressesMax)
			continue
		}

		for _, family := range []struct {
			got      []protocol.PeerRecordAddress
			expected []string
		}{{record.IPv4, test.ipv4}, {record.IPv6, test.ipv6}} {
			if len(family.got) != len(family.expected) {
				t.Errorf("allow local %t IPv4 %t IPv6 %t max %d: %d addresses, expected %d", test.allowLocal, test.allowIPv4, test.allowIPv6, test.addressesMax, len(family.got), len(family.expected))
				continue
			}
			for n := range family.got {
				if !family.got[n].IP.Equal(net.ParseIP(family.expected[n])) {
					t.Errorf("allow local %t IPv4 %t IPv6 %t max %d: address %s, expected %s", test.allowLocal, test.allowIPv4, test.allowIPv6, test.addressesMax, family.got[n].IP.String(), family.expected[n])
				}
			}
		}
	}

	if record := peer.peer2Record(false, false, false, 3); record != nil {
		t.Errorf("no IP version allowed: unexpected peer record %v", record)
	}
}

// TestLimitPeerRecordsSize checks which addresses are removed to keep the peer records of one request key within the target size.
func TestLimitPeerRecordsSize(t *testing.T) {
	newRecords := func(counts ...[2]int) (records []protocol.PeerRecord) {
		for _, count := range counts {
			records = append(records, protocol.PeerRecord{IPv4: make([]protocol.PeerRecordAddress, count[0]), IPv6: make([]protocol.PeerRecordAddress, count[1])})
		}
		return records
	}

	tests := []struct {
		counts   [][2]int // count of IPv4 and IPv6 addresses per peer record
		expected [][2]int
		size     int
	}{
		// 5 records with one address per family are exactly within the target size
		{[][2]int{{1, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}}, [][2]int{{1, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}}, 330},
		{[][2]int{{2, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}}, [][2]int{{2, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}}, 340},
		// few records keep all their addresses
		{[][2]int{{3, 3}, {3, 3}}, [][2]int{{3, 3}, {3, 3}}, 260},
		{[][2]int{{3, 3}, {3, 3}, {1, 1}}, [][2]int{{3, 3}, {3, 3}, {1, 1}}, 326},
		// the third addresses are removed first, starting with the last record
		{[][2]int{{3, 3}, {3, 3}, {3, 3}}, [][2]int{{3, 3}, {3, 2}, {2, 2}}, 336},
		{[][2]int{{3, 3}, {3, 3}, {3, 3}, {3, 3}}, [][2]int{{2, 2}, {2, 2}, {2, 1}, {1, 1}}, 338},
		{[][2]int{{3, 3}, {3, 3}, {3, 3}, {3, 3}, {3, 3}}, [][2]int{{2, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}}, 340},
		{[][2]int{{1, 1}, {1, 1}, {1, 1}, {1, 1}, {3, 3}}, [][2]int{{1, 1}, {1, 1}, {1, 1}, {1, 1}, {2, 1}}, 340},
		{[][2]int{{3, 0}, {0, 3}, {3, 0}, {0, 3}, {3, 3}}, [][2]int{{2, 0}, {0, 2}, {2, 0}, {0, 2}, {2, 1}}, 340},
		// one address per family is kept
		{[][2]int{{1, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}}, [][2]int{{1, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}}, 396},
		{nil, nil, 0},
	}

	for n, test := range tests {
		records := newRecords(test.counts...)
		limitPeerRecordsSize(records, respondPeerRecordsSizeTarget)

		size := 0
		for m := range records {
			size += records[m].Size()
			if len(records[m].IPv4) != test.expected[m][0] || len(records[m].IPv6) != test.expected[m][1] {
				t.Errorf("test %d record %d: %d IPv4 and %d IPv6 addresses, expected %d and %d", n, m, len(records[m].IPv4), len(records[m].IPv6), test.expected[m][0], test.expected[m][1])
			}
		}
		if size != test.size {
			t.Errorf("test %d: size %d, expected %d", n, size, test.size)
		}
	}
}
