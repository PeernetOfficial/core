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

	"github.com/PeernetOfficial/core/blockchain"
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
