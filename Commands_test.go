/*
File Name:  Commands_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"github.com/PeernetOfficial/core/blockchain"
	"github.com/PeernetOfficial/core/dht"
	"github.com/PeernetOfficial/core/protocol"
	"github.com/PeernetOfficial/core/store"
)

// testBlockchain returns a blockchain with the count of blocks, held in memory.
func testBlockchain(t *testing.T, privateKey ed25519.PrivateKey, height int) (result *blockchain.Blockchain) {
	result, err := blockchain.InitStore(privateKey, store.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}

	for n := 0; n < height; n++ {
		if _, _, status := result.Append([]blockchain.BlockRecordRaw{{Type: blockchain.RecordTypeProfile, Data: []byte{byte(n)}}}); status != blockchain.StatusOK {
			t.Fatalf("appending block %d: status %d", n, status)
		}
	}

	if _, got, _ := result.Header(); got != uint64(height) {
		t.Fatalf("blockchain of height %d, expected %d", got, height)
	}

	return result
}

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
// up to 3 per family in A_RESPONSE_SELF and A_RESPONSE_PEER, at most one per family in B_RESPONSE_BLOCKCHAIN.
// A peer known only by link-local and loopback addresses is not reported.
// The peer lists itself as storing peer for the own blockchain only; the blockchain cache holds a copy of the blockchain of another peer.
func TestAnnouncementResponseAddresses(t *testing.T) {
	self, selfPrivateKey, _ := ed25519.GenerateKey(rand.Reader)

	backend := &Backend{peerPublicKey: self, peerPrivateKey: selfPrivateKey, UserBlockchain: testBlockchain(t, selfPrivateKey, 1), networks: &Networks{}}
	backend.initFilters()
	backend.nodesDHT = dht.NewDHT(&dht.Node{ID: self}, 256, bucketSize, alpha, beta, gamma)

	peerMany := testPeerConnections(t, backend, "203.0.113.1", "203.0.113.2", "203.0.113.3", "203.0.113.4", "2001:db8::1", "2001:db8::2", "2001:db8::3", "2001:db8::4", "192.168.1.1", "127.0.0.1", "fe80::1")
	peerOne := testPeerConnections(t, backend, "203.0.113.5", "2001:db8::5")
	peerNotShared := testPeerConnections(t, backend, "127.0.0.1", "169.254.1.6", "::1", "fe80::6")
	peerLocal := testPeerConnections(t, backend, "192.168.1.7")

	for _, peer := range []*PeerInfo{peerMany, peerOne, peerNotShared, peerLocal} {
		backend.nodesDHT.AddNode(&dht.Node{ID: peer.PublicKey, Info: peer})
	}

	// The blockchain cache holds a copy of the blockchain of peerOne.
	backend.GlobalBlockchainCache = &BlockchainCache{backend: backend, Store: &blockchain.MultiStore{Database: store.NewMemoryStore()}}
	if _, err := backend.GlobalBlockchainCache.Store.NewBlockchainHeader(peerOne.PublicKey, 1, 5); err != nil {
		t.Fatal(err)
	}
	if _, found, err := backend.GlobalBlockchainCache.Store.ReadBlockchainHeader(peerOne.PublicKey); err != nil || !found {
		t.Fatalf("blockchain header in the cache: found %t (%v)", found, err)
	}

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

			// Its blockchain record is signed by it at the current time.
			if !protocol.BlockchainRecordValid(self, record.Key, storing.BlockchainVersion, storing.BlockchainHeight, storing.Time, storing.Signature, time.Now()) {
				t.Errorf("record %d: blockchain record of the storing peer is not valid", n)
			}
			if age := time.Now().Unix() - int64(storing.Time); age < 0 || age > 60 {
				t.Errorf("record %d: blockchain record of the storing peer is %d seconds old", n, age)
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

// TestAnnouncementResponseBlockchain checks the peers close to the key in the response to B_FIND_BLOCKCHAIN: the gamma closest peers of the routing table,
// spread over response records of at most 10, in packets within 1232 bytes. The storing peer is in the first record, the last record indicates the last result.
func TestAnnouncementResponseBlockchain(t *testing.T) {
	self, selfPrivateKey, _ := ed25519.GenerateKey(rand.Reader)

	backend := &Backend{peerPublicKey: self, peerPrivateKey: selfPrivateKey, UserBlockchain: testBlockchain(t, selfPrivateKey, 1), networks: &Networks{}}
	backend.initFilters()

	var records []protocol.ResponseRecord
	backend.Filters.MessageOutResponse = func(peer *PeerInfo, packet *protocol.PacketRaw, recordsOut []protocol.ResponseRecord) {
		records = recordsOut
	}

	requester := testPeerConnections(t, backend)
	other, _, _ := ed25519.GenerateKey(rand.Reader)

	for _, test := range []struct {
		peers   int // count of peers in the routing table
		key     []byte
		closest []int // count of closest peers per response record
	}{
		{0, self, []int{0}},
		{4, self, []int{4}},
		{10, other, []int{10}},
		{12, self, []int{10, 2}},
		{20, other, []int{10, 10}},
		{30, self, []int{10, 10}},
		{30, other, []int{10, 10}},
	} {
		backend.nodesDHT = dht.NewDHT(&dht.Node{ID: self}, 256, bucketSize, alpha, beta, gamma)
		for n := 0; n < test.peers; n++ {
			peer := testPeerConnections(t, backend, "203.0.113.1", "2001:db8::1", "203.0.113.2", "2001:db8::2")
			backend.nodesDHT.AddNode(&dht.Node{ID: peer.PublicKey, Info: peer})
		}
		// A bucket holds 20 peers: of 30 peers at least 20 are in the routing table.
		if count := backend.nodesDHT.NumNodes(); count != test.peers && (test.peers <= 20 || count < 20) {
			t.Fatalf("%d peers: the routing table holds %d", test.peers, count)
		}

		announcement := &protocol.MessageAnnouncement{MessageRaw: &protocol.MessageRaw{}, Features: 1<<protocol.FeatureIPv4Listen | 1<<protocol.FeatureIPv6Listen, UserAgent: "Test Client/1.0"}
		announcement.FindBlockchain = [][]byte{test.key}

		records = nil
		requester.cmdAnouncement(announcement, nil)

		if len(records) != len(test.closest) {
			t.Fatalf("%d peers: %d response records, expected %d", test.peers, len(records), len(test.closest))
		}

		var closest []protocol.PeerRecord

		for n, record := range records {
			if record.Type != protocol.ResponseBBlockchain || !bytes.Equal(record.Key, test.key) || len(record.Closest) != test.closest[n] || record.IsLast != (n == len(records)-1) {
				t.Fatalf("%d peers: record %d of type %d with %d closest peers, last %t, expected %d closest peers", test.peers, n, record.Type, len(record.Closest), record.IsLast, test.closest[n])
			}
			if storing := n == 0 && bytes.Equal(test.key, self); storing != (len(record.Storing) == 1) {
				t.Fatalf("%d peers: record %d with %d storing peers", test.peers, n, len(record.Storing))
			}
			for _, peer := range record.Closest {
				if len(peer.IPv4) != 1 || len(peer.IPv6) != 1 {
					t.Fatalf("%d peers: closest peer with %d IPv4 and %d IPv6 addresses", test.peers, len(peer.IPv4), len(peer.IPv6))
				}
			}
			closest = append(closest, record.Closest...)
		}

		// The closest peers are the closest ones of the routing table, closest first.
		expected := backend.nodesDHT.GetClosestContacts(gamma, test.key, nil)
		if len(closest) != len(expected) {
			t.Fatalf("%d peers: %d closest peers, expected %d", test.peers, len(closest), len(expected))
		}
		for n := range expected {
			if !bytes.Equal(closest[n].PublicKey, expected[n].ID) {
				t.Fatalf("%d peers: closest peer %d is not the one of the routing table", test.peers, n)
			}
		}

		// The response encodes into packets within 1232 bytes, and decodes.
		packets, err := protocol.EncodeResponse(true, records, 0, 0, 0, "Test Client/1.0")
		if err != nil {
			t.Fatalf("%d peers: encoding the response: %v", test.peers, err)
		}

		decoded := 0
		for n, packet := range packets {
			if size := len(packet) + protocol.PacketLengthMin; size > 1232 {
				t.Fatalf("%d peers: packet %d of %d bytes", test.peers, n, size)
			}

			msg := &protocol.MessageRaw{PacketRaw: protocol.PacketRaw{Command: protocol.CommandResponse, Payload: packet}}
			response, err := protocol.DecodeResponse(msg)
			if err != nil {
				t.Fatalf("%d peers: decoding the response: %v", test.peers, err)
			}
			for _, record := range response.Records {
				decoded += len(record.Closest)
			}
		}
		if decoded != len(expected) {
			t.Fatalf("%d peers: %d closest peers decoded, expected %d", test.peers, decoded, len(expected))
		}
	}
}

// TestAnnouncementResponseBlockchainHeight checks when the peer lists itself as storing peer in the response to B_FIND_BLOCKCHAIN for the own blockchain:
// if the blockchain has a block. The peers close to the key are returned in both cases.
func TestAnnouncementResponseBlockchainHeight(t *testing.T) {
	for _, height := range []int{0, 1, 3} {
		self, selfPrivateKey, _ := ed25519.GenerateKey(rand.Reader)

		backend := &Backend{peerPublicKey: self, peerPrivateKey: selfPrivateKey, UserBlockchain: testBlockchain(t, selfPrivateKey, height), networks: &Networks{}}
		backend.initFilters()
		backend.nodesDHT = dht.NewDHT(&dht.Node{ID: self}, 256, bucketSize, alpha, beta, gamma)

		for n := 0; n < 3; n++ {
			peer := testPeerConnections(t, backend, "203.0.113.1")
			backend.nodesDHT.AddNode(&dht.Node{ID: peer.PublicKey, Info: peer})
		}

		var records []protocol.ResponseRecord
		backend.Filters.MessageOutResponse = func(peer *PeerInfo, packet *protocol.PacketRaw, recordsOut []protocol.ResponseRecord) {
			records = recordsOut
		}

		announcement := &protocol.MessageAnnouncement{MessageRaw: &protocol.MessageRaw{}, Features: 1 << protocol.FeatureIPv4Listen}
		announcement.FindBlockchain = [][]byte{self}

		testPeerConnections(t, backend).cmdAnouncement(announcement, nil)

		if len(records) != 1 || !records[0].IsLast || len(records[0].Closest) != 3 {
			t.Fatalf("height %d: %d response records", height, len(records))
		}

		expected := 0
		if height > 0 {
			expected = 1
		}
		if len(records[0].Storing) != expected {
			t.Fatalf("height %d: %d storing peers, expected %d", height, len(records[0].Storing), expected)
		}
		for _, storing := range records[0].Storing {
			if !storing.PublicKey.Equal(self) || storing.BlockchainHeight != uint64(height) || storing.BlockchainVersion != 0 {
				t.Fatalf("height %d: storing peer of version %d, height %d", height, storing.BlockchainVersion, storing.BlockchainHeight)
			}
		}
	}
}

// TestStoreBlockchainValid checks which blockchain records of a B_STORE_BLOCKCHAIN request are handled: those signed by the sender with a time that is valid.
func TestStoreBlockchainValid(t *testing.T) {
	self, _, _ := ed25519.GenerateKey(rand.Reader)

	backend := &Backend{peerPublicKey: self, UserBlockchain: &blockchain.Blockchain{}, networks: &Networks{}}
	backend.initFilters()
	backend.nodesDHT = dht.NewDHT(&dht.Node{ID: self}, 256, bucketSize, alpha, beta, gamma)

	var handled []*protocol.BlockchainStore
	backend.Filters.IncomingRequest = func(peer *PeerInfo, RequestType int, Key []byte, Info interface{}) {
		if RequestType == protocol.RequestBStoreBlockchain {
			handled = append(handled, Info.(*protocol.BlockchainStore))
		}
	}

	senderPublicKey, senderPrivateKey, _ := ed25519.GenerateKey(rand.Reader)
	_, otherPrivateKey, _ := ed25519.GenerateKey(rand.Reader)
	sender := &PeerInfo{Backend: backend, PublicKey: senderPublicKey}

	now := time.Now().Unix()

	// record returns a blockchain record with a time relative to now, signed by the key.
	record := func(privateKey ed25519.PrivateKey, version, height uint64, timeOffset int64) (record protocol.BlockchainStore) {
		blockchainPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)
		record = protocol.BlockchainStore{PublicKey: blockchainPublicKey, Version: version, Height: height, Time: uint32(now + timeOffset)}
		record.Signature = protocol.BlockchainRecordSign(privateKey, record.PublicKey, record.Version, record.Height, record.Time)
		return record
	}

	changedHeight := record(senderPrivateKey, 1, 4, 0)
	changedHeight.Height++

	announcement := &protocol.MessageAnnouncement{MessageRaw: &protocol.MessageRaw{}}
	announcement.StoreBlockchain = []protocol.BlockchainStore{
		record(senderPrivateKey, 1, 1, 0),        // valid
		record(otherPrivateKey, 1, 2, 0),         // signed by another peer
		{PublicKey: self, Version: 1, Height: 3}, // without signature and time
		changedHeight,                            // height changed after signing
		record(senderPrivateKey, 1, 5, -25*3600), // 25 hours in the past
		record(senderPrivateKey, 1, 6, 2*3600),   // 2 hours in the future
		record(senderPrivateKey, 1, 7, -23*3600), // valid, 23 hours in the past
		record(senderPrivateKey, 1, 8, 1800),     // valid, 30 minutes in the future
	}

	sender.cmdAnouncement(announcement, nil)

	if len(handled) != 3 || handled[0].Height != 1 || handled[1].Height != 7 || handled[2].Height != 8 {
		heights := []uint64{}
		for _, record := range handled {
			heights = append(heights, record.Height)
		}
		t.Fatalf("blockchain records handled: heights %v, expected 1, 7 and 8", heights)
	}
}

// TestPeer2Record checks the peer record of a peer by what may be shared.
func TestPeer2Record(t *testing.T) {
	peer := testPeerConnections(t, nil, "203.0.113.1", "192.168.1.1", "203.0.113.2", "10.0.0.1", "172.16.0.1", "2001:db8::1", "fd00::1", "127.0.0.1", "169.254.1.1", "::1", "fe80::1")

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
		// 5 records with one address per family are within the target size
		{[][2]int{{1, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}}, [][2]int{{1, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}}, 330},
		{[][2]int{{2, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}}, [][2]int{{2, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1}}, 340},
		{[][2]int{{2, 1}, {2, 1}, {2, 1}, {2, 1}, {2, 1}}, [][2]int{{2, 1}, {2, 1}, {2, 1}, {2, 1}, {2, 1}}, 380},
		// few records keep all their addresses
		{[][2]int{{3, 3}, {3, 3}}, [][2]int{{3, 3}, {3, 3}}, 260},
		{[][2]int{{3, 3}, {3, 3}, {1, 1}}, [][2]int{{3, 3}, {3, 3}, {1, 1}}, 326},
		// the third addresses are removed first, starting with the last record
		{[][2]int{{3, 3}, {3, 3}, {3, 3}}, [][2]int{{3, 3}, {3, 3}, {3, 2}}, 368},
		{[][2]int{{3, 3}, {3, 3}, {3, 3}, {3, 3}}, [][2]int{{2, 2}, {2, 2}, {2, 2}, {2, 1}}, 370},
		{[][2]int{{3, 3}, {3, 3}, {3, 3}, {3, 3}, {3, 3}}, [][2]int{{2, 2}, {2, 1}, {1, 1}, {1, 1}, {1, 1}}, 372},
		{[][2]int{{1, 1}, {1, 1}, {1, 1}, {1, 1}, {3, 3}}, [][2]int{{1, 1}, {1, 1}, {1, 1}, {1, 1}, {3, 2}}, 372},
		{[][2]int{{3, 0}, {0, 3}, {3, 0}, {0, 3}, {3, 3}}, [][2]int{{3, 0}, {0, 2}, {2, 0}, {0, 2}, {2, 2}}, 372},
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
