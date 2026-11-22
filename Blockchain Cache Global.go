/*
File Name:  Blockchain Cache Global.go
Copyright:  2021 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"time"

	"github.com/PeernetOfficial/core/blockchain"
	"github.com/PeernetOfficial/core/protocol"
	"github.com/enfipy/locker"
)

// blockchainCacheRetry is the time after which the blockchain cache checks again whether it holds all blocks of the blockchain of a peer,
// and requests the missing ones. A change of the version or height that the peer reports is checked at once.
const blockchainCacheRetry = 10 * time.Minute

// The blockchain cache stores blockchains.
type BlockchainCache struct {
	BlockchainDirectory string // The directory for storing blockchains in a key-value store.
	MaxBlockCount       uint64 // Max count of blocks to cache per blockchain.
	LimitTotalRecords   uint64 // Max count of blocks and header in total to keep across all blockchains. 0 = unlimited. Max Records * Max Block Size = Size Limit.
	ReadOnly            bool   // Whether the cache is read only.

	Store    *blockchain.MultiStore
	peerLock *locker.Locker

	backend *Backend
}

// initBlockchainCache opens the blockchain cache. The search index must be initialized before.
func (backend *Backend) initBlockchainCache() {
	if backend.Config.BlockchainGlobal == "" {
		return
	}

	backend.GlobalBlockchainCache = &BlockchainCache{
		backend:             backend,
		BlockchainDirectory: backend.Config.BlockchainGlobal,
		MaxBlockCount:       backend.Config.CacheMaxBlockCount,
		LimitTotalRecords:   backend.Config.LimitTotalRecords,
	}

	// The blockchains of a cache in another format are deleted. They are removed from the search index.
	store, deleted, err := blockchain.InitMultiStore(backend.Config.BlockchainGlobal)
	if err != nil {
		backend.LogError("initBlockchainCache", "initializing database '%s': %s", backend.Config.BlockchainGlobal, err.Error())
		backend.GlobalBlockchainCache = nil
		return
	}
	backend.GlobalBlockchainCache.Store = store

	for _, publicKey := range deleted {
		backend.SearchIndex.UnindexBlockchain(publicKey)
	}

	backend.GlobalBlockchainCache.peerLock = locker.Initialize()

	// Set the blockchain cache to read-only if the record limit is reached.
	if backend.Config.LimitTotalRecords > 0 && backend.GlobalBlockchainCache.Store.Database.Count() >= backend.Config.LimitTotalRecords {
		backend.GlobalBlockchainCache.ReadOnly = true
	}

	backend.GlobalBlockchainCache.Store.FilterStatisticUpdate = backend.Filters.GlobalBlockchainCacheStatistic
	backend.GlobalBlockchainCache.Store.FilterBlockchainDelete = backend.Filters.GlobalBlockchainCacheDelete
}

// SeenBlockchainVersion shall be called with information about another peer's blockchain.
// It requests the blocks that the cache does not hold yet, up to the height the peer reports and the max count of blocks per blockchain.
// If the reported version number is newer, all existing blocks are immediately deleted.
func (cache *BlockchainCache) SeenBlockchainVersion(peer *PeerInfo) {
	cache.peerLock.Lock(string(peer.PublicKey))
	defer cache.peerLock.Unlock(string(peer.PublicKey))

	version, height := peer.BlockchainVersion, peer.BlockchainHeight

	header, found, err := cache.Store.ReadBlockchainHeader(peer.PublicKey)
	if err != nil {
		return
	}

	if found && (header.Version > version || header.Version == version && header.Height > height) {
		// The cache holds a newer version or more blocks than the peer reports.
		cache.Store.DeleteBlockchain(header)
		cache.backend.SearchIndex.UnindexBlockchain(peer.PublicKey)
		return
	} else if found && header.Version < version {
		// A new version of the blockchain: Delete the existing blocks first.
		cache.Store.DeleteBlockchain(header)
		cache.backend.SearchIndex.UnindexBlockchain(peer.PublicKey)
		found = false
	}

	if !found {
		header = blockchain.NewBlockchainHeader(peer.PublicKey, version)
	}

	if height > cache.MaxBlockCount {
		height = cache.MaxBlockCount
	}
	if header.Height < height {
		cache.download(peer, header, height)
	}

	if cache.LimitTotalRecords > 0 {
		// Bug: This code is currently never reached if ReadOnly is true.
		cache.ReadOnly = cache.Store.Database.Count() >= cache.LimitTotalRecords
	}
}

// download requests the blocks from the height of the header up to the given height from the peer and stores them.
// The blocks are stored in the order of their numbers. The download ends at the first block that the peer does not send or that is not valid as next block.
func (cache *BlockchainCache) download(peer *PeerInfo, header *blockchain.MultiBlockchainHeader, height uint64) {
	target := protocol.BlockRange{Offset: header.Height, Limit: height - header.Height}

	conn, _, err := peer.BlockTransferRequest(peer.PublicKey, target.Limit, blockchain.MaxBlockSize, []protocol.BlockRange{target})
	if err != nil {
		return
	}
	defer conn.Close()

	for header.Height < height {
		data, _, _, availability, err := protocol.BlockTransferReadBlock(conn, blockchain.MaxBlockSize)
		if err != nil || availability != protocol.GetBlockStatusAvailable {
			return
		}

		decoded, err := cache.Store.IngestBlock(header, data)
		if err != nil {
			return
		}

		// index it for search
		cache.backend.SearchIndex.IndexNewBlockDecoded(peer.PublicKey, header.Version, decoded.Number, decoded.RecordsDecoded)
	}
}

// remoteBlockchainUpdate shall be called for each message of the peer that reports its blockchain version and height.
// It starts updating the copy of the blockchain in the blockchain cache if the version or height changed (isUpdate),
// or if the last update was started longer ago than blockchainCacheRetry. One update runs at a time per peer.
// This function is called in the Go routine of the packet worker and therefore must not stall.
func (peer *PeerInfo) remoteBlockchainUpdate(isUpdate bool) {
	cache := peer.Backend.GlobalBlockchainCache
	if cache == nil || cache.ReadOnly || peer.BlockchainVersion == 0 && peer.BlockchainHeight == 0 {
		return
	}

	peer.blockchainCacheMutex.Lock()
	defer peer.blockchainCacheMutex.Unlock()

	if peer.blockchainCacheChanged = peer.blockchainCacheChanged || isUpdate; peer.blockchainCacheRunning {
		return
	} else if !peer.blockchainCacheChanged && time.Since(peer.blockchainCacheLast) < blockchainCacheRetry {
		return
	}

	peer.blockchainCacheRunning = true

	go func() {
		for {
			peer.blockchainCacheMutex.Lock()
			peer.blockchainCacheChanged = false
			peer.blockchainCacheLast = time.Now()
			peer.blockchainCacheMutex.Unlock()

			cache.SeenBlockchainVersion(peer)

			// A change that was reported while the update ran is processed right after it.
			peer.blockchainCacheMutex.Lock()
			if !peer.blockchainCacheChanged {
				peer.blockchainCacheRunning = false
				peer.blockchainCacheMutex.Unlock()
				return
			}
			peer.blockchainCacheMutex.Unlock()
		}
	}()
}
