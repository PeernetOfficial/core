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
	mathrand "math/rand"
	"net"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/PeernetOfficial/core/blockchain"
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

	// listed returns the storing peers listed for the blockchain as "peer:height", sorted.
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
		sort.Strings(result)
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
	check("record of a second peer", key1, "A:2 B:4")

	peerB.store(key2, 7, 5, now)
	check("second blockchain", key2, "B:5")
	check("first blockchain unchanged", key1, "A:2 B:4")

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
// at most the limit, without the requester, and with the addresses that may be shared with the requester. The storing peers are picked at random among the listings.
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

	// check checks the storing peers returned: their count, and that each is another one of the listings given, with the addresses given.
	check := func(name string, requesterPublicKey ed25519.PublicKey, allowLocal, allowIPv4, allowIPv6 bool, limit int, at time.Time, count int, listings []address) {
		t.Helper()

		storing := backend.listingsStoring(key, requesterPublicKey, allowLocal, allowIPv4, allowIPv6, limit, at)
		if len(storing) != count {
			t.Fatalf("%s: %d storing peers, expected %d", name, len(storing), count)
		}

		expected := make(map[uint64]address)
		for _, listing := range listings {
			expected[listing.height] = listing
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

			listing, ok := expected[record.BlockchainHeight]
			if !ok {
				t.Fatalf("%s: storing peer %d of height %d is not expected, or returned twice", name, n, record.BlockchainHeight)
			}
			delete(expected, record.BlockchainHeight)

			if ipv4 != listing.ipv4 || ipv6 != listing.ipv6 {
				t.Fatalf("%s: storing peer %d of height %d with addresses '%s' '%s', expected '%s' '%s'", name, n, record.BlockchainHeight, ipv4, ipv6, listing.ipv4, listing.ipv6)
			}
			if !protocol.BlockchainRecordValid(record.PublicKey, key, record.BlockchainVersion, record.BlockchainHeight, record.Time, record.Signature, at) {
				t.Fatalf("%s: blockchain record of storing peer %d is not valid", name, n)
			}
		}
	}

	listingOld := address{1, "203.0.113.1", "2001:db8::1"}
	listingNew := address{2, "203.0.113.2", "2001:db8::2"}
	listingMid := address{3, "203.0.113.3", ""}
	public := []address{listingOld, listingNew, listingMid}

	// A peer that is only known by a local address is not shared over a connection that is not local.
	check("all", requester, false, true, true, 10, now, 3, public)
	check("limit 2", requester, false, true, true, 2, now, 2, public)
	check("limit 1", requester, false, true, true, 1, now, 1, public)
	check("limit 0", requester, false, true, true, 0, now, 0, nil)

	// Over a local connection the local address is shared in preference.
	check("local", requester, true, true, true, 10, now, 4, []address{{2, "192.168.1.2", "2001:db8::2"}, listingMid, listingOld, {4, "192.168.1.4", ""}})

	// Only addresses of the IP versions the requester listens on. A storing peer without such an address is left out and does not count towards the limit.
	check("IPv4 only", requester, false, true, false, 10, now, 3, []address{{2, "203.0.113.2", ""}, {3, "203.0.113.3", ""}, {1, "203.0.113.1", ""}})
	check("IPv6 only", requester, false, false, true, 2, now, 2, []address{{2, "", "2001:db8::2"}, {1, "", "2001:db8::1"}})

	// The requester is not reported to itself.
	check("requester is listed", peerNew.peer.PublicKey, false, true, true, 10, now, 2, []address{listingMid, listingOld})

	// Every listing is picked about as often as every other one: with a limit of 1, each of the 3 about 200 times in 600 answers.
	picked := make(map[uint64]int)
	for n := 0; n < 600; n++ {
		storing := backend.listingsStoring(key, requester, false, true, true, 1, now)
		if len(storing) != 1 {
			t.Fatalf("random pick: %d storing peers, expected 1", len(storing))
		}
		picked[storing[0].BlockchainHeight]++
	}
	for _, listing := range public {
		if picked[listing.height] < 130 || picked[listing.height] > 270 {
			t.Fatalf("random pick: the listing of height %d was picked %d times in 600 answers, expected about 200", listing.height, picked[listing.height])
		}
	}

	// A listing is reported for 24 hours after the time of its blockchain record.
	timeOld := time.Unix(now.Add(-3*time.Hour).Unix(), 0)
	check("24 hours after the oldest record", requester, false, true, true, 10, timeOld.Add(24*time.Hour), 3, public)
	check("24 hours and 1 second after the oldest record", requester, false, true, true, 10, timeOld.Add(24*time.Hour+time.Second), 2, []address{listingNew, listingMid})
	check("26 hours later", requester, false, true, true, 10, now.Add(26*time.Hour), 0, nil)

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

// TestListingsStoringRecent checks that the listings whose blockchain record is at most 13 hours old are reported first, picked at random,
// and that the other listings follow, picked at random.
func TestListingsStoringRecent(t *testing.T) {
	backend := testListingBackend(t, 0)
	now := time.Unix(time.Now().Unix(), 0)

	key, _, _ := ed25519.GenerateKey(rand.Reader)
	keyOlder, _, _ := ed25519.GenerateKey(rand.Reader)
	requester, _, _ := ed25519.GenerateKey(rand.Reader)

	// The height names the listing: 1 and 2 are recent, 3 to 5 are older.
	for height, age := range map[uint64]time.Duration{1: time.Hour, 2: 13 * time.Hour, 3: 13*time.Hour + time.Second, 4: 20 * time.Hour, 5: 24 * time.Hour} {
		testStoringPeerNew(t, backend, "203.0.113.1").store(key, 1, height, now.Add(-age))
	}
	for height, age := range map[uint64]time.Duration{6: 14 * time.Hour, 7: 20 * time.Hour} {
		testStoringPeerNew(t, backend, "203.0.113.1").store(keyOlder, 1, height, now.Add(-age))
	}

	// pick returns how often each listing was reported in 600 answers, by its height, at each place of the answer. Each answer has count storing peers.
	pick := func(name string, blockchainKey ed25519.PublicKey, limit, count int) (picked []map[uint64]int) {
		t.Helper()

		picked = make([]map[uint64]int, count)
		for n := range picked {
			picked[n] = make(map[uint64]int)
		}

		for n := 0; n < 600; n++ {
			storing := backend.listingsStoring(blockchainKey, requester, false, true, true, limit, now)
			if len(storing) != count {
				t.Fatalf("%s: %d storing peers, expected %d", name, len(storing), count)
			}
			for place, record := range storing {
				picked[place][record.BlockchainHeight]++
			}
		}

		return picked
	}

	// expect checks that the listings given were reported about equally often and no other one was reported: together in all 600 answers.
	expect := func(name string, picked map[uint64]int, heights ...uint64) {
		t.Helper()

		total := 0
		for _, height := range heights {
			if share := 600 / len(heights); picked[height] < share-75 || picked[height] > share+75 {
				t.Fatalf("%s: the listing of height %d was reported %d times in 600 answers, expected about %d", name, height, picked[height], share)
			}
			total += picked[height]
		}
		if total != 600 || len(picked) != len(heights) {
			t.Fatalf("%s: unexpected listings reported: %v", name, picked)
		}
	}

	picked := pick("limit 1", key, 1, 1)
	expect("limit 1", picked[0], 1, 2)

	picked = pick("limit 2", key, 2, 2)
	expect("limit 2, first", picked[0], 1, 2)
	expect("limit 2, second", picked[1], 1, 2)

	picked = pick("limit 3", key, 3, 3)
	expect("limit 3, first", picked[0], 1, 2)
	expect("limit 3, second", picked[1], 1, 2)
	expect("limit 3, third", picked[2], 3, 4, 5)

	picked = pick("limit 10", key, 10, 5)
	expect("limit 10, second", picked[1], 1, 2)
	expect("limit 10, third", picked[2], 3, 4, 5)
	expect("limit 10, fifth", picked[4], 3, 4, 5)

	// Without a recent listing the older ones are reported.
	picked = pick("older listings only, limit 1", keyOlder, 1, 1)
	expect("older listings only, limit 1", picked[0], 6, 7)
}

// TestAnnouncementResponseCached checks when the peer lists itself as storing peer in the response to B_FIND_BLOCKCHAIN for the blockchain of another peer:
// if the blockchain cache holds a copy of it with a block. The peer comes first, with a blockchain record over the version and height of the copy, and one listing follows.
func TestAnnouncementResponseCached(t *testing.T) {
	backend := testListingBackend(t, 1)
	self := backend.peerPublicKey
	now := time.Now()

	other, _, _ := ed25519.GenerateKey(rand.Reader)

	peer1 := testStoringPeerNew(t, backend, "203.0.113.1")
	peer2 := testStoringPeerNew(t, backend, "203.0.113.2")
	peer1.store(other, 1, 1, now.Add(-2*time.Hour))
	peer2.store(other, 1, 2, now.Add(-1*time.Hour))

	var records []protocol.ResponseRecord
	backend.Filters.MessageOutResponse = func(peer *PeerInfo, packet *protocol.PacketRaw, recordsOut []protocol.ResponseRecord) {
		records = recordsOut
	}

	requester := testPeerConnections(t, backend)

	// expect checks the storing peers in the response to a find request for the blockchain: the two listings, or the peer itself and one of the listings.
	expect := func(name string, cached bool, version, height uint64) {
		t.Helper()

		announcement := &protocol.MessageAnnouncement{MessageRaw: &protocol.MessageRaw{}, Features: 1 << protocol.FeatureIPv4Listen}
		announcement.FindBlockchain = [][]byte{other}

		records = nil
		requester.cmdAnouncement(announcement, nil)

		if len(records) != 1 {
			t.Fatalf("%s: %d response records", name, len(records))
		}

		storing := records[0].Storing
		if len(storing) != 2 {
			t.Fatalf("%s: %d storing peers, expected 2", name, len(storing))
		}

		isListed := func(n int) bool {
			return storing[n].PublicKey.Equal(peer1.peer.PublicKey) || storing[n].PublicKey.Equal(peer2.peer.PublicKey)
		}
		if cached && !storing[0].PublicKey.Equal(self) || !cached && !isListed(0) || !isListed(1) || storing[0].PublicKey.Equal(storing[1].PublicKey) {
			t.Fatalf("%s: the storing peers are not the expected ones", name)
		}

		if !cached {
			return
		}

		// The peer lists itself without an address. Its blockchain record is signed by it at the current time.
		if len(storing[0].IPv4) != 0 || len(storing[0].IPv6) != 0 {
			t.Errorf("%s: the peer lists itself with %d IPv4 and %d IPv6 addresses", name, len(storing[0].IPv4), len(storing[0].IPv6))
		}
		if storing[0].BlockchainVersion != version || storing[0].BlockchainHeight != height {
			t.Errorf("%s: the peer lists itself with version %d, height %d, expected version %d, height %d", name, storing[0].BlockchainVersion, storing[0].BlockchainHeight, version, height)
		}
		if !protocol.BlockchainRecordValid(self, other, storing[0].BlockchainVersion, storing[0].BlockchainHeight, storing[0].Time, storing[0].Signature, time.Now()) {
			t.Errorf("%s: the blockchain record of the peer is not valid", name)
		}
	}

	expect("without blockchain cache", false, 0, 0)

	cache := testBlockchainCache(t, backend)
	expect("no copy", false, 0, 0)

	testCacheKeep(t, cache, other)
	expect("reason set, no block stored", false, 0, 0)

	// The version and the height of the copy differ.
	version := uint64(1 + mathrand.Intn(100))
	height := version + 1 + uint64(mathrand.Intn(100))
	t.Logf("copy of version %d, height %d", version, height)

	header := &blockchain.MultiBlockchainHeader{PublicKey: other, Version: version, Height: height}
	if err := cache.Store.WriteBlockchainHeader(header); err != nil {
		t.Fatal(err)
	}
	expect("copy", true, version, height)

	header.Version, header.Height = version+1, 0
	if err := cache.Store.WriteBlockchainHeader(header); err != nil {
		t.Fatal(err)
	}
	expect("copy without a block", false, 0, 0)

	header.Height = 2
	if err := cache.Store.WriteBlockchainHeader(header); err != nil {
		t.Fatal(err)
	}
	expect("copy of a new version", true, version+1, 2)

	cache.Store.DeleteBlockchain(header)
	expect("copy deleted", false, 0, 0)
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

	isListed := func(publicKey ed25519.PublicKey) bool {
		return publicKey.Equal(peer1.peer.PublicKey) || publicKey.Equal(peer2.peer.PublicKey) || publicKey.Equal(peer3.peer.PublicKey)
	}

	// selfFirst is whether the peer itself is the first storing peer. Every other storing peer is one of the listed peers.
	for _, test := range []struct {
		name      string
		key       ed25519.PublicKey
		count     int
		selfFirst bool
	}{
		{"own blockchain", self, 2, true},
		{"other blockchain", other, 2, false},
		{"blockchain without listing", unknown, 0, false},
	} {
		announcement := &protocol.MessageAnnouncement{MessageRaw: &protocol.MessageRaw{}, Features: 1<<protocol.FeatureIPv4Listen | 1<<protocol.FeatureIPv6Listen, UserAgent: userAgent}
		announcement.FindBlockchain = [][]byte{test.key}

		records = nil
		requester.cmdAnouncement(announcement, nil)

		if len(records) != 2 || len(records[0].Closest) != 10 || len(records[1].Closest) != 10 || len(records[1].Storing) != 0 {
			t.Fatalf("%s: %d response records", test.name, len(records))
		}
		if len(records[0].Storing) != test.count {
			t.Fatalf("%s: %d storing peers, expected %d", test.name, len(records[0].Storing), test.count)
		}
		for n, record := range records[0].Storing {
			if n == 0 && test.selfFirst != record.PublicKey.Equal(self) || n > 0 && !isListed(record.PublicKey) || n == 1 && record.PublicKey.Equal(records[0].Storing[0].PublicKey) {
				t.Fatalf("%s: storing peer %d is not an expected one", test.name, n)
			}
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

		if len(storing) != test.count {
			t.Fatalf("%s: %d storing peers decoded, expected %d", test.name, len(storing), test.count)
		}
		for n, record := range storing {
			if !bytes.Equal(record.PublicKey, records[0].Storing[n].PublicKey) {
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
