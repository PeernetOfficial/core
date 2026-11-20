/*
File Name:  Blockchain Listing_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/PeernetOfficial/core/dht"
	"github.com/PeernetOfficial/core/protocol"
)

// testListingBackend returns a backend without peers and without network. Its blockchain has the given height.
func testListingBackend(t *testing.T, height int) (backend *Backend) {
	self, selfPrivateKey, _ := ed25519.GenerateKey(rand.Reader)

	backend = &Backend{peerPublicKey: self, peerPrivateKey: selfPrivateKey, UserBlockchain: testBlockchain(t, selfPrivateKey, height), networks: &Networks{}}
	backend.initFilters()
	backend.nodesDHT = dht.NewDHT(&dht.Node{ID: self}, 256, bucketSize, alpha, beta, gamma)

	return backend
}

// testStoringPeer is a peer that sends store requests in a test.
type testStoringPeer struct {
	peer       *PeerInfo
	privateKey ed25519.PrivateKey
}

// testStoringPeerNew returns a peer with active connections to the IPs, and its private key.
func testStoringPeerNew(t *testing.T, backend *Backend, ips ...string) (storing *testStoringPeer) {
	peer := testPeerConnections(t, backend, ips...)
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	peer.PublicKey = publicKey

	return &testStoringPeer{peer: peer, privateKey: privateKey}
}

// store lets the backend handle a B_STORE_BLOCKCHAIN request of the peer with one blockchain record, signed by the peer.
func (storing *testStoringPeer) store(blockchainPublicKey ed25519.PublicKey, version, height uint64, recordTime time.Time) {
	record := protocol.BlockchainStore{PublicKey: blockchainPublicKey, Version: version, Height: height, Time: uint32(recordTime.Unix())}
	record.Signature = protocol.BlockchainRecordSign(storing.privateKey, record.PublicKey, record.Version, record.Height, record.Time)

	announcement := &protocol.MessageAnnouncement{MessageRaw: &protocol.MessageRaw{}}
	announcement.StoreBlockchain = []protocol.BlockchainStore{record}

	storing.peer.cmdAnouncement(announcement, nil)
}

// testListingsCount returns the count of blockchains with a listing and the count of listings.
func testListingsCount(backend *Backend) (blockchains, listings int) {
	backend.listings.RLock()
	defer backend.listings.RUnlock()

	for _, storing := range backend.listings.blockchains {
		listings += len(storing)
	}

	return len(backend.listings.blockchains), listings
}

// TestListingAdd checks which store requests are kept as listing: a valid blockchain record of a peer that has an address to share.
// A later record of the same peer for the same blockchain replaces the listing, an earlier one does not.
func TestListingAdd(t *testing.T) {
	backend := testListingBackend(t, 0)
	now := time.Now()

	key1, _, _ := ed25519.GenerateKey(rand.Reader)
	key2, _, _ := ed25519.GenerateKey(rand.Reader)
	requester, _, _ := ed25519.GenerateKey(rand.Reader)

	peerA := testStoringPeerNew(t, backend, "203.0.113.1")
	peerB := testStoringPeerNew(t, backend, "203.0.113.2")
	peerNoAddress := testStoringPeerNew(t, backend)
	peerLoopback := testStoringPeerNew(t, backend, "127.0.0.1")

	// listed returns the storing peers listed for the blockchain as "peer:height", the newest record first.
	listed := func(key ed25519.PublicKey) string {
		var result []string
		for _, storing := range backend.listingsStoring(key, requester, false, true, true, 10, now) {
			name := "?"
			switch {
			case storing.PublicKey.Equal(peerA.peer.PublicKey):
				name = "A"
			case storing.PublicKey.Equal(peerB.peer.PublicKey):
				name = "B"
			}
			if !protocol.BlockchainRecordValid(storing.PublicKey, key, storing.BlockchainVersion, storing.BlockchainHeight, storing.Time, storing.Signature, now) {
				name += "(invalid)"
			}
			result = append(result, name+":"+string(rune('0'+storing.BlockchainHeight)))
		}
		return strings.Join(result, " ")
	}

	check := func(name string, key ed25519.PublicKey, expected string) {
		t.Helper()
		if got := listed(key); got != expected {
			t.Fatalf("%s: listed '%s', expected '%s'", name, got, expected)
		}
	}

	check("no store request", key1, "")

	peerA.store(key1, 1, 1, now.Add(-time.Hour))
	check("valid record", key1, "A:1")
	check("valid record, other blockchain", key2, "")

	peerA.store(key1, 1, 2, now.Add(-30*time.Minute))
	check("later record of the same peer", key1, "A:2")

	peerA.store(key1, 1, 3, now.Add(-45*time.Minute))
	check("earlier record of the same peer", key1, "A:2")

	peerB.store(key1, 1, 4, now.Add(-10*time.Minute))
	check("record of a second peer", key1, "B:4 A:2")

	peerB.store(key2, 7, 5, now)
	check("second blockchain", key2, "B:5")
	check("first blockchain unchanged", key1, "B:4 A:2")

	// Records that are not valid, and peers without an address to share.
	peerA.store(key2, 1, 6, now.Add(-25*time.Hour))
	peerA.store(key2, 1, 6, now.Add(2*time.Hour))
	peerNoAddress.store(key2, 1, 7, now)
	peerLoopback.store(key2, 1, 8, now)

	announcement := &protocol.MessageAnnouncement{MessageRaw: &protocol.MessageRaw{}}
	announcement.StoreBlockchain = []protocol.BlockchainStore{{PublicKey: key2, Version: 1, Height: 9, Time: uint32(now.Unix())}}
	peerA.peer.cmdAnouncement(announcement, nil)

	check("records not kept", key2, "B:5")

	if blockchains, listings := testListingsCount(backend); blockchains != 2 || listings != 3 {
		t.Fatalf("%d blockchains with %d listings, expected 2 with 3", blockchains, listings)
	}
}

// TestListingsStoring checks the storing peer records returned for a listing: for 24 hours after the time of the blockchain record,
// at most the limit and the newest record first, without the requester, and with the addresses that may be shared with the requester.
func TestListingsStoring(t *testing.T) {
	backend := testListingBackend(t, 0)
	now := time.Now()

	key, _, _ := ed25519.GenerateKey(rand.Reader)
	requester, _, _ := ed25519.GenerateKey(rand.Reader)

	peerOld := testStoringPeerNew(t, backend, "203.0.113.1", "2001:db8::1")
	peerNew := testStoringPeerNew(t, backend, "192.168.1.2", "203.0.113.2", "2001:db8::2")
	peerMid := testStoringPeerNew(t, backend, "203.0.113.3")
	peerLocal := testStoringPeerNew(t, backend, "192.168.1.4")

	peerOld.store(key, 1, 1, now.Add(-3*time.Hour))
	peerNew.store(key, 1, 2, now.Add(-1*time.Hour))
	peerMid.store(key, 1, 3, now.Add(-2*time.Hour))
	peerLocal.store(key, 1, 4, now.Add(-4*time.Hour))

	type address struct {
		height     uint64
		ipv4, ipv6 string
	}

	check := func(name string, requesterPublicKey ed25519.PublicKey, allowLocal, allowIPv4, allowIPv6 bool, limit int, at time.Time, expected []address) {
		t.Helper()

		storing := backend.listingsStoring(key, requesterPublicKey, allowLocal, allowIPv4, allowIPv6, limit, at)
		if len(storing) != len(expected) {
			t.Fatalf("%s: %d storing peers, expected %d", name, len(storing), len(expected))
		}

		for n, record := range storing {
			var ipv4, ipv6 string
			if len(record.IPv4) > 1 || len(record.IPv6) > 1 {
				t.Fatalf("%s: storing peer %d with %d IPv4 and %d IPv6 addresses", name, n, len(record.IPv4), len(record.IPv6))
			}
			if len(record.IPv4) == 1 {
				ipv4 = record.IPv4[0].IP.String()
			}
			if len(record.IPv6) == 1 {
				ipv6 = record.IPv6[0].IP.String()
			}

			if record.BlockchainHeight != expected[n].height || ipv4 != expected[n].ipv4 || ipv6 != expected[n].ipv6 {
				t.Fatalf("%s: storing peer %d of height %d with addresses '%s' '%s', expected height %d with '%s' '%s'", name, n, record.BlockchainHeight, ipv4, ipv6, expected[n].height, expected[n].ipv4, expected[n].ipv6)
			}
			if !protocol.BlockchainRecordValid(record.PublicKey, key, record.BlockchainVersion, record.BlockchainHeight, record.Time, record.Signature, at) {
				t.Fatalf("%s: blockchain record of storing peer %d is not valid", name, n)
			}
		}
	}

	// The newest record first. A peer that is only known by a local address is not shared over a connection that is not local.
	check("all", requester, false, true, true, 10, now, []address{{2, "203.0.113.2", "2001:db8::2"}, {3, "203.0.113.3", ""}, {1, "203.0.113.1", "2001:db8::1"}})
	check("limit 2", requester, false, true, true, 2, now, []address{{2, "203.0.113.2", "2001:db8::2"}, {3, "203.0.113.3", ""}})
	check("limit 1", requester, false, true, true, 1, now, []address{{2, "203.0.113.2", "2001:db8::2"}})
	check("limit 0", requester, false, true, true, 0, now, nil)

	// Over a local connection the local address is shared in preference.
	check("local", requester, true, true, true, 10, now, []address{{2, "192.168.1.2", "2001:db8::2"}, {3, "203.0.113.3", ""}, {1, "203.0.113.1", "2001:db8::1"}, {4, "192.168.1.4", ""}})

	// Only addresses of the IP versions the requester listens on. A storing peer without such an address is left out and does not count towards the limit.
	check("IPv4 only", requester, false, true, false, 10, now, []address{{2, "203.0.113.2", ""}, {3, "203.0.113.3", ""}, {1, "203.0.113.1", ""}})
	check("IPv6 only", requester, false, false, true, 2, now, []address{{2, "", "2001:db8::2"}, {1, "", "2001:db8::1"}})

	// The requester is not reported to itself.
	check("requester is listed", peerNew.peer.PublicKey, false, true, true, 2, now, []address{{3, "203.0.113.3", ""}, {1, "203.0.113.1", "2001:db8::1"}})

	// A listing is reported for 24 hours after the time of its blockchain record.
	timeOld := time.Unix(now.Add(-3*time.Hour).Unix(), 0)
	check("24 hours after the oldest record", requester, false, true, true, 10, timeOld.Add(24*time.Hour), []address{{2, "203.0.113.2", "2001:db8::2"}, {3, "203.0.113.3", ""}, {1, "203.0.113.1", "2001:db8::1"}})
	check("24 hours and 1 second after the oldest record", requester, false, true, true, 10, timeOld.Add(24*time.Hour+time.Second), []address{{2, "203.0.113.2", "2001:db8::2"}, {3, "203.0.113.3", ""}})
	check("26 hours later", requester, false, true, true, 10, now.Add(26*time.Hour), nil)

	// Listings that are no longer valid are removed.
	if blockchains, listings := testListingsCount(backend); blockchains != 1 || listings != 4 {
		t.Fatalf("%d blockchains with %d listings, expected 1 with 4", blockchains, listings)
	}

	backend.listingsRemoveExpired(timeOld.Add(24 * time.Hour))
	if blockchains, listings := testListingsCount(backend); blockchains != 1 || listings != 3 {
		t.Fatalf("24 hours after the oldest record: %d blockchains with %d listings, expected 1 with 3", blockchains, listings)
	}

	backend.listingsRemoveExpired(timeOld.Add(24*time.Hour + time.Second))
	if blockchains, listings := testListingsCount(backend); blockchains != 1 || listings != 2 {
		t.Fatalf("24 hours and 1 second after the oldest record: %d blockchains with %d listings, expected 1 with 2", blockchains, listings)
	}

	backend.listingsRemoveExpired(now.Add(26 * time.Hour))
	if blockchains, listings := testListingsCount(backend); blockchains != 0 || listings != 0 {
		t.Fatalf("26 hours later: %d blockchains with %d listings, expected none", blockchains, listings)
	}
}

// TestAnnouncementResponseListing checks the storing peers in the response to B_FIND_BLOCKCHAIN: at most 2 per key, in the first response record for the key.
// For the own blockchain the peer itself comes first. With 20 close peers, 2 storing peers and a User Agent of 143 bytes every packet stays within 1232 bytes.
func TestAnnouncementResponseListing(t *testing.T) {
	backend := testListingBackend(t, 1)
	self := backend.peerPublicKey
	now := time.Now()

	other, _, _ := ed25519.GenerateKey(rand.Reader)
	unknown, _, _ := ed25519.GenerateKey(rand.Reader)

	for n := 0; n < 20; n++ {
		peer := testPeerConnections(t, backend, "203.0.113.1", "2001:db8::1")
		backend.nodesDHT.AddNode(&dht.Node{ID: peer.PublicKey, Info: peer})
	}

	peer1 := testStoringPeerNew(t, backend, "203.0.113.1", "2001:db8::1")
	peer2 := testStoringPeerNew(t, backend, "203.0.113.2", "2001:db8::2")
	peer3 := testStoringPeerNew(t, backend, "203.0.113.3", "2001:db8::3")

	for _, key := range []ed25519.PublicKey{self, other} {
		peer1.store(key, 1, 1, now.Add(-3*time.Hour))
		peer2.store(key, 1, 2, now.Add(-1*time.Hour))
		peer3.store(key, 1, 3, now.Add(-2*time.Hour))
	}

	var records []protocol.ResponseRecord
	backend.Filters.MessageOutResponse = func(peer *PeerInfo, packet *protocol.PacketRaw, recordsOut []protocol.ResponseRecord) {
		records = recordsOut
	}

	requester := testPeerConnections(t, backend)
	userAgent := strings.Repeat("a", 143)

	for _, test := range []struct {
		name    string
		key     ed25519.PublicKey
		storing []ed25519.PublicKey
	}{
		{"own blockchain", self, []ed25519.PublicKey{self, peer2.peer.PublicKey}},
		{"other blockchain", other, []ed25519.PublicKey{peer2.peer.PublicKey, peer3.peer.PublicKey}},
		{"blockchain without listing", unknown, nil},
	} {
		announcement := &protocol.MessageAnnouncement{MessageRaw: &protocol.MessageRaw{}, Features: 1<<protocol.FeatureIPv4Listen | 1<<protocol.FeatureIPv6Listen, UserAgent: userAgent}
		announcement.FindBlockchain = [][]byte{test.key}

		records = nil
		requester.cmdAnouncement(announcement, nil)

		if len(records) != 2 || len(records[0].Closest) != 10 || len(records[1].Closest) != 10 || len(records[1].Storing) != 0 {
			t.Fatalf("%s: %d response records", test.name, len(records))
		}
		if len(records[0].Storing) != len(test.storing) {
			t.Fatalf("%s: %d storing peers, expected %d", test.name, len(records[0].Storing), len(test.storing))
		}

		// The response encodes into packets within 1232 bytes. The decoded storing peers are the expected ones, with a valid blockchain record.
		packets, err := protocol.EncodeResponse(true, records, 0, 0, 0, userAgent)
		if err != nil || len(packets) != 2 {
			t.Fatalf("%s: encoding the response: %d packets (%v)", test.name, len(packets), err)
		}

		var storing []protocol.StoringPeer
		for n, packet := range packets {
			if size := len(packet) + protocol.PacketLengthMin; size > 1232 {
				t.Fatalf("%s: packet %d of %d bytes", test.name, n, size)
			}

			response, err := protocol.DecodeResponse(&protocol.MessageRaw{PacketRaw: protocol.PacketRaw{Command: protocol.CommandResponse, Payload: packet}})
			if err != nil {
				t.Fatalf("%s: decoding the response: %v", test.name, err)
			}
			for _, record := range response.Records {
				storing = append(storing, record.Storing...)
			}
		}

		if len(storing) != len(test.storing) {
			t.Fatalf("%s: %d storing peers decoded, expected %d", test.name, len(storing), len(test.storing))
		}
		for n, record := range storing {
			if !bytes.Equal(record.PublicKey, test.storing[n]) {
				t.Fatalf("%s: storing peer %d is not the expected one", test.name, n)
			}
			if !protocol.BlockchainRecordValid(record.PublicKey, test.key, record.BlockchainVersion, record.BlockchainHeight, record.Time, record.Signature, now) {
				t.Fatalf("%s: blockchain record of storing peer %d is not valid", test.name, n)
			}

			// The peer itself has no address, a listed peer one per family.
			if addresses := len(record.IPv4) + len(record.IPv6); record.PublicKey.Equal(self) && addresses != 0 || !record.PublicKey.Equal(self) && addresses != 2 {
				t.Fatalf("%s: storing peer %d with %d addresses", test.name, n, addresses)
			}
		}
	}

	// With the first packet at its largest, the size is the one the limit of 2 storing peers is computed from: 1232 bytes.
	announcement := &protocol.MessageAnnouncement{MessageRaw: &protocol.MessageRaw{}, Features: 1<<protocol.FeatureIPv4Listen | 1<<protocol.FeatureIPv6Listen}
	announcement.FindBlockchain = [][]byte{other}
	requester.cmdAnouncement(announcement, nil)

	packets, err := protocol.EncodeResponse(true, records, 0, 0, 0, userAgent)
	if err != nil || len(packets) != 2 || len(packets[0])+protocol.PacketLengthMin != 1232 {
		t.Fatalf("largest first packet: %d packets, first of %d bytes (%v)", len(packets), len(packets[0])+protocol.PacketLengthMin, err)
	}
}

// TestAnnouncementResponseListingLocal checks that a storing peer that is only known by a local address is reported in the response to B_FIND_BLOCKCHAIN
// if the announcement arrived over a local connection.
func TestAnnouncementResponseListingLocal(t *testing.T) {
	backend, network := testBackendLoopback(t)
	now := time.Now()

	key, _, _ := ed25519.GenerateKey(rand.Reader)
	testStoringPeerNew(t, backend, "192.168.1.4").store(key, 1, 1, now)

	var records []protocol.ResponseRecord
	backend.Filters.MessageOutResponse = func(peer *PeerInfo, packet *protocol.PacketRaw, recordsOut []protocol.ResponseRecord) {
		records = recordsOut
	}

	requester := testPeerConnections(t, backend)

	for _, test := range []struct {
		ip       string
		expected int
	}{
		{"203.0.113.9", 0},
		{"192.168.1.9", 1},
	} {
		announcement := &protocol.MessageAnnouncement{MessageRaw: &protocol.MessageRaw{}, Features: 1 << protocol.FeatureIPv4Listen}
		announcement.FindBlockchain = [][]byte{key}

		records = nil
		requester.cmdAnouncement(announcement, &Connection{backend: backend, Network: network, Address: &net.UDPAddr{IP: net.ParseIP(test.ip), Port: 112}})

		if len(records) != 1 || len(records[0].Storing) != test.expected {
			t.Fatalf("announcement from %s: %d response records, expected 1 with %d storing peers", test.ip, len(records), test.expected)
		}
		for _, storing := range records[0].Storing {
			if len(storing.IPv4) != 1 || storing.IPv4[0].IP.String() != "192.168.1.4" {
				t.Fatalf("announcement from %s: unexpected address of the storing peer", test.ip)
			}
		}
	}
}
