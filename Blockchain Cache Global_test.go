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

// testBlockchainCache gives the backend an empty blockchain cache held in memory.
func testBlockchainCache(t *testing.T, backend *Backend) (cache *BlockchainCache) {
	multi, _, err := blockchain.InitMultiStoreDatabase(store.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}

	backend.GlobalBlockchainCache = &BlockchainCache{backend: backend, Store: multi, MaxBlockCount: 256, peerLock: locker.Initialize()}

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
// The height of the copy is the count of blocks it holds, block 0 to height-1, up to the max count of blocks per blockchain. Blocks that are missing are requested with the next update.
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

	// The copy ends at the max count of blocks per blockchain, also when the blockchain grows further.
	cache.MaxBlockCount = height + 3
	testBlockchainAppend(t, owner, 4)
	update("blockchain above the max count", 0, height+6, 0, height+3)
	testBlockchainAppend(t, owner, 2)
	update("blockchain grows above the max count", 0, height+8, 0, height+3)

	// With a higher max count the download continues at the height of the copy.
	cache.MaxBlockCount = 256
	update("higher max count", 0, height+8, 0, height+8)

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
