/*
File Name:  Blockchain Cache Global_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"bytes"
	mathrand "math/rand"
	"testing"
	"time"

	"github.com/PeernetOfficial/core/blockchain"
	"github.com/PeernetOfficial/core/protocol"
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
	testBlockchainCache(t, backend)
	peer, privateKey, socket := testPeerSocket(t, backend, network)

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
