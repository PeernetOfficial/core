/*
File Name:  Blockchain Cache Global_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	mathrand "math/rand"
	"testing"
	"time"
	"uuid"

	"github.com/PeernetOfficial/core/blockchain"
	"github.com/PeernetOfficial/core/protocol"
	"github.com/PeernetOfficial/core/search"
	"github.com/PeernetOfficial/core/store"
	"github.com/enfipy/locker"
)

// testCacheMaxBlockchainSize is the max size of one cached blockchain in the tests, in bytes.
const testCacheMaxBlockchainSize = 256 * blockchain.MaxBlockSize

// testBlockchainCache gives the backend an empty blockchain cache held in memory. Up to 4 blockchains are downloaded at the same time.
func testBlockchainCache(t *testing.T, backend *Backend) (cache *BlockchainCache) {
	multi, _, err := blockchain.InitMultiStoreDatabase(store.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}

	backend.GlobalBlockchainCache = &BlockchainCache{backend: backend, Store: multi, MaxBlockchainSize: testCacheMaxBlockchainSize, peerLock: locker.Initialize(), downloads: make(chan struct{}, 4)}

	return backend.GlobalBlockchainCache
}

// testReason is the reason for which the tests keep a blockchain in the cache.
const testReason = 1

// testCacheKeep sets a reason to keep the blockchain in the cache for an hour.
func testCacheKeep(t *testing.T, cache *BlockchainCache, publicKey ed25519.PublicKey) {
	t.Helper()

	if err := cache.Keep(publicKey, testReason, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("setting the reason: %v", err)
	}
}

// testBlockchainAppend appends the count of blocks to the blockchain of the backend.
func testBlockchainAppend(t *testing.T, backend *Backend, count int) {
	for n := 0; n < count; n++ {
		if _, _, status := backend.UserBlockchain.Append([]blockchain.BlockRecordRaw{{Type: blockchain.RecordTypeProfile, Data: []byte{0, 0, byte(n)}}}); status != blockchain.StatusOK {
			t.Fatalf("appending block: status %d", status)
		}
	}
}

// testCacheExpect checks the copy that the cache holds of the blockchain of the owner: its version and height, and that it holds the blocks 0 to height-1 as the owner has them.
// Height 0 means that the cache holds nothing of the blockchain.
func testCacheExpect(t *testing.T, name string, cache *BlockchainCache, owner *Backend, version, height uint64) {
	t.Helper()

	header, found, err := cache.Store.ReadBlockchainHeader(owner.peerPublicKey)
	if err != nil {
		t.Fatalf("%s: reading the header: %v", name, err)
	} else if height == 0 {
		if found {
			t.Fatalf("%s: the cache holds version %d, height %d, expected nothing", name, header.Version, header.Height)
		}
		return
	} else if !found {
		t.Fatalf("%s: the cache holds nothing, expected version %d, height %d", name, version, height)
	} else if header.Version != version || header.Height != height {
		t.Fatalf("%s: the cache holds version %d, height %d, expected version %d, height %d", name, header.Version, header.Height, version, height)
	}

	for n := uint64(0); n < height; n++ {
		expected, _, _ := owner.UserBlockchain.GetBlockRaw(n)
		if raw, found := cache.Store.ReadBlock(owner.peerPublicKey, version, n); !found || !bytes.Equal(raw, expected) {
			t.Fatalf("%s: block %d found %t in the cache, or it differs from the block of the owner", name, n, found)
		}
	}
	if _, found := cache.Store.ReadBlock(owner.peerPublicKey, version, height); found {
		t.Fatalf("%s: block %d found in the cache, above the height", name, height)
	}
}

// TestBlockchainCacheHeight checks the copy of a blockchain that the blockchain cache downloads from its owner, through the packet workers of two backends on the loopback address.
// The height of the copy is the count of blocks it holds, block 0 to height-1. Blocks that are missing are requested with the next update.
func TestBlockchainCacheHeight(t *testing.T) {
	testRootPeers(t)

	height := uint64(3 + mathrand.Intn(6))
	t.Logf("blockchain of %d blocks", height)

	backend, network := testBackendLive(t, 0)
	owner, networkOwner := testBackendLive(t, int(height))
	cache := testBlockchainCache(t, backend)
	peer := testConnect(backend, network, owner, networkOwner)
	testCacheKeep(t, cache, owner.peerPublicKey)

	// update reports the version and height to the cache and checks the copy after it.
	update := func(name string, reportVersion, reportHeight, expectVersion, expectHeight uint64) {
		t.Helper()

		peer.BlockchainVersion, peer.BlockchainHeight = reportVersion, reportHeight
		cache.SeenBlockchainVersion(peer)

		testCacheExpect(t, name, cache, owner, expectVersion, expectHeight)
	}

	// The owner reports 2 blocks more than it sends.
	update("owner reports more blocks than it sends", 0, height+2, 0, height)

	// The 2 blocks are available now.
	testBlockchainAppend(t, owner, 2)
	update("missing blocks are available", 0, height+2, 0, height+2)
	update("same height again", 0, height+2, 0, height+2)

	testBlockchainAppend(t, owner, 6)
	update("blockchain grows", 0, height+8, 0, height+8)

	// A new version replaces the copy.
	owner.UserBlockchain.DeleteBlockchain()
	testBlockchainAppend(t, owner, 2)
	update("new version", 1, 2, 1, 2)
	if _, found := cache.Store.ReadBlock(owner.peerPublicKey, 0, 0); found {
		t.Errorf("new version: block 0 of the old version is still in the cache")
	}

	// The copy is deleted if the owner reports a lower height or version than the copy has.
	update("lower height", 1, 1, 0, 0)
	update("download again", 1, 2, 1, 2)
	update("lower version", 0, 5, 0, 0)
}

// TestBlockchainCacheServe checks that a peer answers a block request for a blockchain that it holds a copy of in the blockchain cache, with the blocks of the copy
// and for the version of the copy. The request comes from a third peer, through the packet workers of three backends on the loopback address.
func TestBlockchainCacheServe(t *testing.T) {
	testRootPeers(t)

	height := uint64(4 + mathrand.Intn(5))
	t.Logf("blockchain of %d blocks", height)

	owner, networkOwner := testBackendLive(t, int(height))
	backend, network := testBackendLive(t, 0)
	requester, networkRequester := testBackendLive(t, 0)

	cache := testBlockchainCache(t, backend)
	peerOwner := testConnect(backend, network, owner, networkOwner)
	peer := testConnect(requester, networkRequester, backend, network)

	// request requests the blocks of the blockchain of the owner from the peer with the cache. It returns the blocks it received.
	request := func(name string, version uint64, target protocol.BlockRange, expectRefused bool) (blocks [][]byte) {
		t.Helper()

		blocks, err := testBlockRequest(t, name, peer, owner.peerPublicKey, version, target, 10*time.Second)
		if expectRefused && err == nil {
			t.Fatalf("%s: no error returned, %d blocks received", name, len(blocks))
		} else if !expectRefused && err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		return blocks
	}

	// expect checks the blocks received against the blocks of the owner from the given number on.
	expect := func(name string, blocks [][]byte, number, count uint64) {
		t.Helper()

		if uint64(len(blocks)) != count {
			t.Fatalf("%s: received %d blocks, expected %d", name, len(blocks), count)
		}
		for n, block := range blocks {
			if expected, _, _ := owner.UserBlockchain.GetBlockRaw(number + uint64(n)); !bytes.Equal(block, expected) {
				t.Errorf("%s: block %d differs from the block of the owner", name, number+uint64(n))
			}
		}
	}

	// A reason is set, and no block of the blockchain is stored yet.
	testCacheKeep(t, cache, owner.peerPublicKey)
	request("no block stored", 0, protocol.BlockRange{Offset: 0, Limit: 1}, true)

	// The cache downloads the blockchain.
	peerOwner.BlockchainVersion, peerOwner.BlockchainHeight = 0, height
	cache.SeenBlockchainVersion(peerOwner)
	testCacheExpect(t, "download", cache, owner, 0, height)

	expect("all blocks", request("all blocks", 0, protocol.BlockRange{Offset: 0, Limit: height}, false), 0, height)
	expect("blocks 1 to 2", request("blocks 1 to 2", 0, protocol.BlockRange{Offset: 1, Limit: 2}, false), 1, 2)
	expect("above the height", request("above the height", 0, protocol.BlockRange{Offset: height - 2, Limit: 1 << 40}, false), height-2, 2)
	request("other version", 1, protocol.BlockRange{Offset: 0, Limit: 1}, true)

	// The owner has a new version of its blockchain, and the cache replaces the copy. The blocks of version 0 are no longer read.
	_, read, found := backend.storedBlocks(owner.peerPublicKey, 0)
	if !found {
		t.Fatalf("version 0 of the copy not found")
	} else if _, found = read(0); !found {
		t.Fatalf("block 0 of version 0 of the copy not found")
	}

	owner.UserBlockchain.DeleteBlockchain()
	testBlockchainAppend(t, owner, int(height)+1)
	peerOwner.BlockchainVersion, peerOwner.BlockchainHeight = 1, height+1
	cache.SeenBlockchainVersion(peerOwner)
	testCacheExpect(t, "new version", cache, owner, 1, height+1)

	for n := uint64(0); n < height; n++ {
		if _, found := read(n); found {
			t.Errorf("new version: block %d of version 0 found", n)
		}
	}

	request("version 0 after the new version", 0, protocol.BlockRange{Offset: 0, Limit: 1}, true)
	expect("new version", request("new version", 1, protocol.BlockRange{Offset: 0, Limit: height + 1}, false), 0, height+1)

	// The reason ends and the copy is deleted.
	cache.expire(time.Now().Add(2 * time.Hour))
	testCacheExpect(t, "reason ended", cache, owner, 0, 0)
	request("copy deleted", 1, protocol.BlockRange{Offset: 0, Limit: 1}, true)
}

// testCacheRunning returns whether an update of the copy of the blockchain of the peer is running.
func testCacheRunning(peer *PeerInfo) bool {
	peer.blockchainCacheMutex.Lock()
	defer peer.blockchainCacheMutex.Unlock()

	return peer.blockchainCacheRunning
}

// testCacheWait waits until no update of the copy of the blockchain of the peer is running. It fails the test after the timeout.
func testCacheWait(t *testing.T, name string, peer *PeerInfo, timeout time.Duration) {
	t.Helper()

	for end := time.Now().Add(timeout); testCacheRunning(peer); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("%s: the update is still running after %s", name, timeout)
		}
	}
}

// TestBlockchainCacheUpdate checks when the blockchain cache updates the copy of the blockchain of a peer:
// at once when the peer reports a change of the version or height, otherwise when the last update was started longer ago than blockchainCacheRetry.
// With the later update the cache requests the blocks that the copy is missing.
func TestBlockchainCacheUpdate(t *testing.T) {
	testRootPeers(t)

	height := uint64(3 + mathrand.Intn(6))
	t.Logf("blockchain of %d blocks", height)

	backend, network := testBackendLive(t, 0)
	owner, networkOwner := testBackendLive(t, int(height))
	cache := testBlockchainCache(t, backend)
	peer := testConnect(backend, network, owner, networkOwner)
	testCacheKeep(t, cache, owner.peerPublicKey)

	// The owner reports 2 blocks more than it sends.
	peer.BlockchainVersion, peer.BlockchainHeight = 0, height+2
	peer.remoteBlockchainUpdate(true)
	testCacheWait(t, "change reported", peer, 10*time.Second)
	testCacheExpect(t, "change reported", cache, owner, 0, height)

	// The 2 blocks are available now. Further messages of the owner report the same version and height.
	testBlockchainAppend(t, owner, 2)

	peer.remoteBlockchainUpdate(false)
	if testCacheRunning(peer) {
		t.Fatalf("no change, last update just started: an update was started")
	}
	testCacheExpect(t, "no change, last update just started", cache, owner, 0, height)

	peer.blockchainCacheMutex.Lock()
	peer.blockchainCacheLast = time.Now().Add(-blockchainCacheRetry)
	peer.blockchainCacheMutex.Unlock()

	peer.remoteBlockchainUpdate(false)
	testCacheWait(t, "no change, last update long ago", peer, 10*time.Second)
	testCacheExpect(t, "no change, last update long ago", cache, owner, 0, height+2)
}

// TestBlockchainCacheUpdateRunning checks that one update of the copy of the blockchain of a peer runs at a time.
// A change that the peer reports while an update runs starts the next update when the running one has ended.
// The peer is a socket that does not answer: an update sends one block request and ends when the request times out.
func TestBlockchainCacheUpdateRunning(t *testing.T) {
	testRootPeers(t)

	previous := blockSequenceTimeout
	blockSequenceTimeout = 500 * time.Millisecond
	t.Cleanup(func() { blockSequenceTimeout = previous })

	backend, network := testBackendLive(t, 0)
	cache := testBlockchainCache(t, backend)
	peer, privateKey, socket := testPeerSocket(t, backend, network)
	testCacheKeep(t, cache, peer.PublicKey)

	// requests returns the count of block requests that arrive at the socket of the peer within the time.
	requests := func(wait time.Duration) (count int) {
		for end := time.Now().Add(wait); time.Now().Before(end); {
			if packet := testReadPacket(t, "block request", backend, socket, privateKey, time.Until(end)); packet != nil && packet.Command == protocol.CommandGetBlock {
				count++
			}
		}
		return count
	}

	peer.BlockchainVersion, peer.BlockchainHeight = 0, 5
	peer.remoteBlockchainUpdate(true)
	if count := requests(time.Second); count != 1 || !testCacheRunning(peer) {
		t.Fatalf("change reported: %d block requests, expected 1; update running %t", count, testCacheRunning(peer))
	}

	// Further messages of the peer arrive while the update runs. The last update was started long ago.
	for n := 0; n < 10; n++ {
		peer.blockchainCacheMutex.Lock()
		peer.blockchainCacheLast = time.Now().Add(-blockchainCacheRetry)
		peer.blockchainCacheMutex.Unlock()

		peer.remoteBlockchainUpdate(false)
	}

	// A change is reported while the update runs. The next request follows when the running update has ended.
	peer.BlockchainHeight = 6
	peer.remoteBlockchainUpdate(true)
	if count := requests(time.Second); count != 0 {
		t.Fatalf("messages while the update runs: %d block requests, expected 0", count)
	}
	if packet := testReadPacket(t, "block request", backend, socket, privateKey, 10*time.Second); packet == nil || packet.Command != protocol.CommandGetBlock {
		t.Fatalf("after the running update ended: no block request")
	}

	// No request follows the second update. It ends within 5 seconds: the request times out after 500 ms, which is checked every 4 seconds.
	if count := requests(6 * time.Second); count != 0 || testCacheRunning(peer) {
		t.Fatalf("after the second update: %d block requests, expected 0; update running %t", count, testCacheRunning(peer))
	}
}

// testCacheReported sets a filter that counts the blockchains reported to the client. It returns the counter.
func testCacheReported(backend *Backend) (count *int) {
	count = new(int)
	backend.Filters.BlockchainReported = func(peer *PeerInfo) { *count++ }

	return count
}

// TestBlockchainCacheReasons checks that the blockchain cache keeps a copy of a blockchain while a reason is set for it:
// a blockchain without a reason is reported to the client and not downloaded, a reason that is set starts the download,
// and the copy is deleted and removed from the search index when the last reason has ended.
func TestBlockchainCacheReasons(t *testing.T) {
	testRootPeers(t)

	height := uint64(3 + mathrand.Intn(6))
	t.Logf("blockchain of %d blocks", height)

	backend, network := testBackendLive(t, 0)
	owner, networkOwner := testBackendLive(t, int(height-1))
	cache := testBlockchainCache(t, backend)
	backend.SearchIndex = &search.SearchIndexStore{Database: store.NewMemoryStore()}
	reported := testCacheReported(backend)

	deleted := 0
	cache.Store.FilterBlockchainDelete = func(multi *blockchain.MultiStore, header *blockchain.MultiBlockchainHeader) { deleted++ }

	// The last block of the owner holds a file.
	file := blockchain.BlockRecordFile{Hash: protocol.HashData([]byte("test")), MerkleRootHash: protocol.HashData([]byte("test")), ID: uuid.New(), Size: 4, Tags: []blockchain.BlockRecordFileTag{blockchain.TagFromText(blockchain.TagName, "cachereasons.txt")}}
	if _, _, status := owner.UserBlockchain.AddFiles([]blockchain.BlockRecordFile{file}); status != blockchain.StatusOK {
		t.Fatalf("adding the file: status %d", status)
	}
	searchCount := func() int { return len(backend.SearchIndex.Search("cachereasons")) }

	peer := testConnect(backend, network, owner, networkOwner)
	publicKey := owner.peerPublicKey

	// report passes a message of the owner that reports its blockchain, waits for the update and checks the count of reports to the client and the copy.
	report := func(name string, isUpdate bool, expectReported int, expectHeight uint64) {
		t.Helper()

		peer.BlockchainVersion, peer.BlockchainHeight = 0, height
		peer.remoteBlockchainUpdate(isUpdate)
		testCacheWait(t, name, peer, 10*time.Second)

		if *reported != expectReported {
			t.Fatalf("%s: %d reports to the client, expected %d", name, *reported, expectReported)
		}
		testCacheExpect(t, name, cache, owner, 0, expectHeight)
	}

	// Without a reason the blockchain is reported and not downloaded.
	report("no reason, change", true, 1, 0)
	report("no reason, no change", false, 1, 0)

	peer.blockchainCacheMutex.Lock()
	peer.blockchainCacheReported = time.Now().Add(-blockchainCacheRetry)
	peer.blockchainCacheMutex.Unlock()

	report("no reason, last report long ago", false, 2, 0)
	report("no reason, no change again", false, 2, 0)

	// A reason is set while the owner is in the peer list: the copy is downloaded at once.
	start := time.Now().Truncate(time.Second)
	until1 := start.Add(time.Hour)

	if err := cache.Keep(publicKey, 1, until1); err != nil {
		t.Fatal(err)
	}
	testCacheWait(t, "reason set", peer, 10*time.Second)
	testCacheExpect(t, "reason set", cache, owner, 0, height)

	if count := searchCount(); count != 1 {
		t.Fatalf("reason set: %d search results for the file, expected 1", count)
	}

	if since, until, found := cache.Reason(publicKey, 1); !found || !until.Equal(until1) || since.Before(start) || since.After(time.Now()) {
		t.Fatalf("reason set: found %t, set %s, ends %s", found, since, until)
	} else if _, _, found := cache.Reason(publicKey, 2); found {
		t.Fatalf("reason set: another reason found")
	}

	// A reason that is set again keeps the time it was set and takes the new end.
	since1 := start.Add(-time.Hour)
	if err := cache.Store.WriteReasons(publicKey, map[uint8]blockchain.MultiReason{1: {Since: since1, Until: until1}}); err != nil {
		t.Fatal(err)
	}

	until1 = start.Add(90 * time.Minute)
	until2 := start.Add(2 * time.Hour)

	if err := cache.Keep(publicKey, 1, until1); err != nil {
		t.Fatal(err)
	} else if err := cache.Keep(publicKey, 2, until2); err != nil {
		t.Fatal(err)
	}

	if since, until, found := cache.Reason(publicKey, 1); !found || !since.Equal(since1) || !until.Equal(until1) {
		t.Fatalf("reason set again: found %t, set %s, ends %s", found, since, until)
	} else if _, until, found := cache.Reason(publicKey, 2); !found || !until.Equal(until2) {
		t.Fatalf("second reason: found %t, ends %s", found, until)
	}

	// One of two reasons is ended: the copy stays.
	if err := cache.Drop(publicKey, 1); err != nil {
		t.Fatal(err)
	} else if _, _, found := cache.Reason(publicKey, 1); found {
		t.Fatalf("reason ended: it is found")
	}

	cache.expire(time.Now())
	testCacheExpect(t, "one of two reasons ended", cache, owner, 0, height)

	if reasons, err := cache.Store.ReadReasons(publicKey); err != nil || len(reasons) != 1 || !reasons[2].Until.Equal(until2) {
		t.Fatalf("one of two reasons ended: %d reasons stored (%v), expected the second one", len(reasons), err)
	}

	cache.expire(until2.Add(-time.Second))
	testCacheExpect(t, "before the end of the last reason", cache, owner, 0, height)

	if count := searchCount(); count != 1 || deleted != 0 {
		t.Fatalf("before the end of the last reason: %d search results for the file, %d blockchains deleted", count, deleted)
	}

	// The last reason has ended: the copy is deleted and removed from the search index.
	cache.expire(until2)
	testCacheExpect(t, "last reason ended", cache, owner, 0, 0)

	if count := searchCount(); count != 0 || deleted != 1 {
		t.Fatalf("last reason ended: %d search results for the file, %d blockchains deleted, expected 0 and 1", count, deleted)
	}

	stored := 0
	cache.Store.IterateReasons(func(publicKey ed25519.PublicKey) { stored++ })
	if stored != 0 {
		t.Fatalf("last reason ended: reasons are stored for %d blockchains", stored)
	}

	report("no reason left, change", true, 3, 0)

	// A reason that is set after it ended is a new one. The reason that ended is still stored.
	if err := cache.Store.WriteReasons(publicKey, map[uint8]blockchain.MultiReason{testReason: {Since: start.Add(-2 * time.Hour), Until: start.Add(-time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	testCacheKeep(t, cache, publicKey)
	testCacheWait(t, "reason set anew", peer, 10*time.Second)
	testCacheExpect(t, "reason set anew", cache, owner, 0, height)

	if since, _, found := cache.Reason(publicKey, testReason); !found || since.Before(start) {
		t.Fatalf("reason set anew: found %t, set %s", found, since)
	}

	// Reasons that cannot be read are removed, and the copy with them.
	if err := cache.Store.Database.Set(append(bytes.Clone(publicKey), []byte("reasons")...), make([]byte, 5)); err != nil {
		t.Fatal(err)
	} else if err := cache.Keep(publicKey, testReason, time.Now().Add(time.Hour)); err == nil {
		t.Fatalf("reasons that cannot be read: a reason was set")
	}

	cache.expire(time.Now())
	testCacheExpect(t, "reasons that cannot be read", cache, owner, 0, 0)

	if err := cache.Keep(publicKey, testReason, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("after the reasons were removed: %v", err)
	}
}

// TestBlockchainCacheKeepReported checks that a reason which the client sets when the blockchain of a peer is reported to it starts the download of the copy.
// A reason that has ended when it is set starts none.
func TestBlockchainCacheKeepReported(t *testing.T) {
	testRootPeers(t)

	height := uint64(3 + mathrand.Intn(6))
	t.Logf("blockchain of %d blocks", height)

	backend, network := testBackendLive(t, 0)
	owner, networkOwner := testBackendLive(t, int(height))
	cache := testBlockchainCache(t, backend)
	peer := testConnect(backend, network, owner, networkOwner)

	until := time.Now()
	backend.Filters.BlockchainReported = func(reported *PeerInfo) {
		if reported != peer || reported.BlockchainVersion != 0 || reported.BlockchainHeight != height {
			t.Errorf("reported peer %x, version %d, height %d", reported.PublicKey, reported.BlockchainVersion, reported.BlockchainHeight)
		}
		if err := cache.Keep(reported.PublicKey, testReason, until); err != nil {
			t.Error(err)
		}
	}

	peer.BlockchainVersion, peer.BlockchainHeight = 0, height
	peer.remoteBlockchainUpdate(true)
	testCacheWait(t, "reason that has ended", peer, 10*time.Second)
	testCacheExpect(t, "reason that has ended", cache, owner, 0, 0)

	until = time.Now().Add(time.Hour)
	peer.remoteBlockchainUpdate(true)
	testCacheWait(t, "reason set at the report", peer, 10*time.Second)
	testCacheExpect(t, "reason set at the report", cache, owner, 0, height)

	// A blockchain of another peer is kept for a reason of its own.
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if cache.isKept(other) || !cache.isKept(owner.peerPublicKey) {
		t.Fatalf("kept: other blockchain %t, blockchain of the owner %t", cache.isKept(other), cache.isKept(owner.peerPublicKey))
	}
}

// testBlockchainAppendSize appends the count of blocks to the blockchain of the backend. Each block has a record of a random size between the two sizes.
func testBlockchainAppendSize(t *testing.T, backend *Backend, count, sizeMin, sizeMax int) {
	for n := 0; n < count; n++ {
		data := make([]byte, sizeMin+mathrand.Intn(sizeMax-sizeMin+1))
		rand.Read(data[2:])

		if _, _, status := backend.UserBlockchain.Append([]blockchain.BlockRecordRaw{{Type: blockchain.RecordTypeProfile, Data: data}}); status != blockchain.StatusOK {
			t.Fatalf("appending block: status %d", status)
		}
	}
}

// testBlocksSize returns the size in bytes of the first count blocks of the blockchain of the backend.
func testBlocksSize(t *testing.T, backend *Backend, count uint64) (size uint64) {
	for n := uint64(0); n < count; n++ {
		raw, status, err := backend.UserBlockchain.GetBlockRaw(n)
		if err != nil || status != blockchain.StatusOK {
			t.Fatalf("reading block %d: status %d (%v)", n, status, err)
		}
		size += uint64(len(raw))
	}

	return size
}

// testCacheHeld returns the count of blocks and their size in bytes that the cache holds of the blockchain of the owner, as its header states them.
// It checks that the cache holds these blocks as the owner has them, and that the size is the size of these blocks.
func testCacheHeld(t *testing.T, name string, cache *BlockchainCache, owner *Backend) (height, size uint64) {
	t.Helper()

	header, found, err := cache.Store.ReadBlockchainHeader(owner.peerPublicKey)
	if err != nil {
		t.Fatalf("%s: reading the header: %v", name, err)
	} else if found {
		height, size = header.Height, header.Size
	}

	testCacheExpect(t, name, cache, owner, 0, height)

	if expected := testBlocksSize(t, owner, height); size != expected {
		t.Fatalf("%s: the copy of %d blocks has the size %d, its blocks have %d", name, height, size, expected)
	}

	return height, size
}

// TestBlockchainCacheLimitBlockchain checks the max size of one cached blockchain. A copy takes a further block while it has room for a block of the maximum size:
// a blockchain above the limit is cached from block 0 on up to the limit, and the download continues with a higher limit.
func TestBlockchainCacheLimitBlockchain(t *testing.T) {
	testRootPeers(t)

	count := uint64(12 + mathrand.Intn(8))

	backend, network := testBackendLive(t, 0)
	owner, networkOwner := testBackendLive(t, 0)
	testBlockchainAppendSize(t, owner, int(count), 20000, 32000)
	t.Logf("blockchain of %d blocks, %d bytes", count, testBlocksSize(t, owner, count))

	cache := testBlockchainCache(t, backend)
	peer := testConnect(backend, network, owner, networkOwner)
	testCacheKeep(t, cache, owner.peerPublicKey)

	// update sets the limit, updates the copy and checks it. It returns the count of blocks the copy holds.
	update := func(name string, limit uint64) (height uint64) {
		t.Helper()

		cache.MaxBlockchainSize = limit
		peer.BlockchainVersion, peer.BlockchainHeight = 0, count
		cache.SeenBlockchainVersion(peer)

		height, size := testCacheHeld(t, name, cache, owner)
		t.Logf("%s: limit %d, the copy holds %d blocks, %d bytes", name, limit, height, size)

		if size > limit {
			t.Fatalf("%s: the copy has %d bytes, above the limit of %d", name, size, limit)
		} else if height < count && limit-size >= blockchain.MaxBlockSize {
			t.Fatalf("%s: the copy holds %d of %d blocks and has room for %d bytes", name, height, count, limit-size)
		} else if total := cache.Store.Size(); total != size {
			t.Fatalf("%s: size of the store %d, of its one copy %d", name, total, size)
		}

		return height
	}

	if height := update("limit below the maximum block size", blockchain.MaxBlockSize-1); height != 0 {
		t.Fatalf("limit below the maximum block size: the copy holds %d blocks", height)
	}

	height1 := update("limit of 3 blocks", 3*blockchain.MaxBlockSize+uint64(mathrand.Intn(blockchain.MaxBlockSize)))
	if height1 == 0 || height1 >= count {
		t.Fatalf("limit of 3 blocks: the copy holds %d of %d blocks", height1, count)
	}

	if height := update("same limit again", cache.MaxBlockchainSize); height != height1 {
		t.Fatalf("same limit again: the copy holds %d blocks, before %d", height, height1)
	}

	height2 := update("limit of 7 blocks", 7*blockchain.MaxBlockSize)
	if height2 <= height1 || height2 >= count {
		t.Fatalf("limit of 7 blocks: the copy holds %d of %d blocks, before %d", height2, count, height1)
	}

	if height := update("limit above the size of the blockchain", testCacheMaxBlockchainSize); height != count {
		t.Fatalf("limit above the size of the blockchain: the copy holds %d of %d blocks", height, count)
	}

	// A copy above a lower limit keeps its blocks and takes no further one.
	testBlockchainAppendSize(t, owner, 1, 20000, 32000)

	cache.MaxBlockchainSize = 2 * blockchain.MaxBlockSize
	peer.BlockchainVersion, peer.BlockchainHeight = 0, count+1
	cache.SeenBlockchainVersion(peer)

	if height, _ := testCacheHeld(t, "limit below the size of the copy", cache, owner); height != count {
		t.Fatalf("limit below the size of the copy: the copy holds %d blocks, expected %d", height, count)
	}
}

// TestBlockchainCacheLimitTotal checks the max size of all cached blockchains. A copy takes a further block while the cache has room for a block of the maximum size.
// The room of a copy that is deleted is available again. Downloads reserve their room, so that together they stay within the limit.
func TestBlockchainCacheLimitTotal(t *testing.T) {
	testRootPeers(t)

	backend, network := testBackendLive(t, 0)
	owner1, networkOwner1 := testBackendLive(t, 0)
	owner2, networkOwner2 := testBackendLive(t, 0)
	testBlockchainAppendSize(t, owner1, 6, 26000, 32000)
	testBlockchainAppendSize(t, owner2, 6, 26000, 32000)

	cache := testBlockchainCache(t, backend)
	cache.MaxSize = 8*blockchain.MaxBlockSize + uint64(mathrand.Intn(blockchain.MaxBlockSize))
	t.Logf("blockchains of %d and %d bytes, limit %d", testBlocksSize(t, owner1, 6), testBlocksSize(t, owner2, 6), cache.MaxSize)

	peer1 := testConnect(backend, network, owner1, networkOwner1)
	peer2 := testConnect(backend, network, owner2, networkOwner2)
	peer1.BlockchainVersion, peer1.BlockchainHeight = 0, 6
	peer2.BlockchainVersion, peer2.BlockchainHeight = 0, 6

	if err := cache.Keep(owner1.peerPublicKey, testReason, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	testCacheWait(t, "first blockchain", peer1, 10*time.Second)

	height1, size1 := testCacheHeld(t, "first blockchain", cache, owner1)
	if height1 != 6 || cache.Store.Size() != size1 {
		t.Fatalf("first blockchain: the copy holds %d of 6 blocks, %d bytes; size of the store %d", height1, size1, cache.Store.Size())
	}

	// The second blockchain is cached while the cache has room for a block of the maximum size.
	testCacheKeep(t, cache, owner2.peerPublicKey)
	testCacheWait(t, "second blockchain", peer2, 10*time.Second)

	height2, size2 := testCacheHeld(t, "second blockchain", cache, owner2)
	if height2 == 0 || height2 >= 6 {
		t.Fatalf("second blockchain: the copy holds %d of 6 blocks", height2)
	} else if total := cache.Store.Size(); total != size1+size2 || total > cache.MaxSize || cache.MaxSize-total >= blockchain.MaxBlockSize {
		t.Fatalf("second blockchain: size of the store %d, of the copies %d and %d, limit %d", total, size1, size2, cache.MaxSize)
	}

	// A full cache downloads nothing.
	cache.SeenBlockchainVersion(peer2)
	if height, _ := testCacheHeld(t, "full cache", cache, owner2); height != height2 {
		t.Fatalf("full cache: the copy holds %d blocks, before %d", height, height2)
	}

	// The first copy is deleted when its reason has ended. The second copy is completed.
	if err := cache.Drop(owner1.peerPublicKey, testReason); err != nil {
		t.Fatal(err)
	}
	cache.expire(time.Now())

	if total := cache.Store.Size(); total != size2 {
		t.Fatalf("first copy deleted: size of the store %d, of the copy left %d", total, size2)
	}

	cache.SeenBlockchainVersion(peer2)
	if height, size := testCacheHeld(t, "room available again", cache, owner2); height != 6 || cache.Store.Size() != size {
		t.Fatalf("room available again: the copy holds %d of 6 blocks, %d bytes; size of the store %d", height, size, cache.Store.Size())
	}

	// Room that is reserved is not available to another download.
	if cache.sizeReserved != 0 {
		t.Fatalf("%d bytes are reserved while no download runs", cache.sizeReserved)
	}

	cache.MaxSize = cache.Store.Size() + 12*blockchain.MaxBlockSize + uint64(mathrand.Intn(blockchain.MaxBlockSize))
	publicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	header := blockchain.NewBlockchainHeader(publicKey, 0)

	if count := cache.reserve(header, 10); count != 10 {
		t.Fatalf("room for 12 blocks: reserved for %d of 10", count)
	} else if count := cache.reserve(header, 10); count != 2 {
		t.Fatalf("room for 2 blocks left: reserved for %d of 10", count)
	} else if count := cache.reserve(header, 1); count != 0 {
		t.Fatalf("no room left: reserved for %d", count)
	} else if cache.hasRoom(header) {
		t.Fatalf("no room left: the cache has room")
	}

	cache.release(10)
	if count := cache.reserve(header, 20); count != 10 {
		t.Fatalf("room for 10 blocks released: reserved for %d of 20", count)
	}

	cache.release(12)
	if cache.sizeReserved != 0 {
		t.Fatalf("%d bytes are reserved after all were released", cache.sizeReserved)
	}

	// The copy itself has room for fewer blocks than the cache.
	header.Size = cache.MaxBlockchainSize - 3*blockchain.MaxBlockSize - 1
	cache.MaxSize = 0
	if count := cache.reserve(header, 10); count != 3 {
		t.Fatalf("copy with room for 3 blocks: reserved for %d of 10", count)
	}
	cache.release(3)
}

// TestBlockchainCacheDownloads checks the max count of blockchains downloaded at the same time. With a max count of 1 the second download starts when the first has ended.
// The first owner is a socket that does not answer: its download ends when the block request times out.
func TestBlockchainCacheDownloads(t *testing.T) {
	testRootPeers(t)

	previous := blockSequenceTimeout
	blockSequenceTimeout = 500 * time.Millisecond
	t.Cleanup(func() { blockSequenceTimeout = previous })

	backend, network := testBackendLive(t, 0)
	owner, networkOwner := testBackendLive(t, 3)
	cache := testBlockchainCache(t, backend)
	cache.downloads = make(chan struct{}, 1)

	silent, _, _ := testPeerSocket(t, backend, network)
	peer := testConnect(backend, network, owner, networkOwner)
	testCacheKeep(t, cache, silent.PublicKey)
	testCacheKeep(t, cache, owner.peerPublicKey)

	silent.BlockchainVersion, silent.BlockchainHeight = 0, 5
	silent.remoteBlockchainUpdate(true)

	for end := time.Now().Add(5 * time.Second); len(cache.downloads) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("the first download did not start")
		}
	}

	// The second download waits for the first one.
	peer.BlockchainVersion, peer.BlockchainHeight = 0, 3
	peer.remoteBlockchainUpdate(true)
	time.Sleep(time.Second)

	if !testCacheRunning(silent) || !testCacheRunning(peer) {
		t.Fatalf("after 1 second: first update running %t, second update running %t", testCacheRunning(silent), testCacheRunning(peer))
	}
	testCacheExpect(t, "first download running", cache, owner, 0, 0)

	testCacheWait(t, "first download", silent, 10*time.Second)
	testCacheWait(t, "second download", peer, 10*time.Second)
	testCacheExpect(t, "first download ended", cache, owner, 0, 3)

	if len(cache.downloads) != 0 {
		t.Fatalf("%d downloads are counted while none runs", len(cache.downloads))
	}

	// With a max count of 0 nothing is downloaded.
	cache.downloads = make(chan struct{})
	testBlockchainAppend(t, owner, 2)

	peer.BlockchainVersion, peer.BlockchainHeight = 0, 5
	peer.remoteBlockchainUpdate(true)
	testCacheWait(t, "max count of 0", peer, 10*time.Second)
	testCacheExpect(t, "max count of 0", cache, owner, 0, 3)
}
