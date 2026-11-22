/*
File Name:  Blockchain Cache Global.go
Copyright:  2021 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"crypto/ed25519"
	"sync"
	"time"

	"github.com/PeernetOfficial/core/blockchain"
	"github.com/PeernetOfficial/core/protocol"
	"github.com/enfipy/locker"
)

// blockchainCacheRetry is the time after which the blockchain cache checks again whether it holds all blocks of the blockchain of a peer,
// and requests the missing ones. A change of the version or height that the peer reports is checked at once.
const blockchainCacheRetry = 10 * time.Minute

// blockchainCacheExpire is the interval at which the blockchain cache removes the reasons that have ended and deletes the copies that no reason is left for.
const blockchainCacheExpire = time.Hour

// The blockchain cache stores blockchains.
// A copy of a blockchain is kept while a reason is set for it. The client sets the reasons, see Keep. A copy that no reason is left for is deleted.
type BlockchainCache struct {
	BlockchainDirectory string // The directory for storing blockchains in a key-value store.
	MaxBlockchainSize   uint64 // Max size of one cached blockchain in bytes.
	MaxSize             uint64 // Max size of all cached blockchains in bytes. 0 = unlimited.

	Store    *blockchain.MultiStore
	peerLock *locker.Locker

	downloads chan struct{} // Holds one entry per blockchain that is downloaded. Its capacity is the max count of blockchains downloaded at the same time.

	sizeMutex    sync.Mutex // Mutex for sizeReserved.
	sizeReserved uint64     // Size in bytes that running downloads have reserved in the cache.

	reasonsMutex sync.Mutex // Mutex for reading and writing the reasons in the store.

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
		MaxBlockchainSize:   backend.Config.CacheMaxBlockchainSize,
		MaxSize:             backend.Config.CacheMaxSize,
		downloads:           make(chan struct{}, backend.Config.CacheMaxDownloads),
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

	backend.GlobalBlockchainCache.Store.FilterStatisticUpdate = backend.Filters.GlobalBlockchainCacheStatistic
	backend.GlobalBlockchainCache.Store.FilterBlockchainDelete = backend.Filters.GlobalBlockchainCacheDelete

	go backend.GlobalBlockchainCache.autoExpire()
}

// Keep sets a reason for which the blockchain is kept in the cache, until the given time. A reason is a number; its meaning is the client's.
// Setting a reason that has not ended changes the time it ends and leaves the time it was set.
// If the blockchain was kept for no reason before and its owner is in the peer list, the copy is updated at once.
func (cache *BlockchainCache) Keep(publicKey ed25519.PublicKey, reason uint8, until time.Time) (err error) {
	now := time.Now()

	cache.reasonsMutex.Lock()

	reasons, err := cache.Store.ReadReasons(publicKey)
	if err != nil {
		cache.reasonsMutex.Unlock()
		return err
	}

	kept := false
	for _, existing := range reasons {
		kept = kept || existing.Until.After(now)
	}

	since := now
	if existing, ok := reasons[reason]; ok && existing.Until.After(now) {
		since = existing.Since
	}
	reasons[reason] = blockchain.MultiReason{Since: since, Until: until}

	err = cache.Store.WriteReasons(publicKey, reasons)
	cache.reasonsMutex.Unlock()

	if err != nil {
		return err
	}

	if !kept && until.After(now) {
		if peer := cache.backend.PeerlistLookup(publicKey); peer != nil {
			peer.blockchainCacheUpdate(true)
		}
	}

	return nil
}

// Drop ends a reason for which the blockchain is kept in the cache.
func (cache *BlockchainCache) Drop(publicKey ed25519.PublicKey, reason uint8) (err error) {
	now := time.Now()

	cache.reasonsMutex.Lock()
	defer cache.reasonsMutex.Unlock()

	reasons, err := cache.Store.ReadReasons(publicKey)
	if err != nil {
		return err
	}

	existing, ok := reasons[reason]
	if !ok || !existing.Until.After(now) {
		return nil
	}
	reasons[reason] = blockchain.MultiReason{Since: existing.Since, Until: now}

	return cache.Store.WriteReasons(publicKey, reasons)
}

// Reason returns the time a reason for which the blockchain is kept in the cache was set, and the time it ends. A reason is found until it ends.
func (cache *BlockchainCache) Reason(publicKey ed25519.PublicKey, reason uint8) (since, until time.Time, found bool) {
	cache.reasonsMutex.Lock()
	defer cache.reasonsMutex.Unlock()

	reasons, err := cache.Store.ReadReasons(publicKey)
	if err != nil {
		return since, until, false
	}

	existing, ok := reasons[reason]
	if !ok || !existing.Until.After(time.Now()) {
		return since, until, false
	}

	return existing.Since, existing.Until, true
}

// isKept returns whether a reason is set for the blockchain that has not ended.
func (cache *BlockchainCache) isKept(publicKey ed25519.PublicKey) bool {
	cache.reasonsMutex.Lock()
	defer cache.reasonsMutex.Unlock()

	reasons, err := cache.Store.ReadReasons(publicKey)
	if err != nil {
		return false
	}

	now := time.Now()
	for _, existing := range reasons {
		if existing.Until.After(now) {
			return true
		}
	}

	return false
}

// autoExpire removes the reasons that have ended and deletes the copies that no reason is left for, at start and every hour.
func (cache *BlockchainCache) autoExpire() {
	for {
		cache.expire(time.Now())

		time.Sleep(blockchainCacheExpire)
	}
}

// expire removes the reasons that have ended at the time now. The copy of a blockchain that no reason is left for is deleted and removed from the search index.
func (cache *BlockchainCache) expire(now time.Time) {
	var publicKeys []ed25519.PublicKey
	cache.Store.IterateReasons(func(publicKey ed25519.PublicKey) {
		publicKeys = append(publicKeys, publicKey)
	})

	for _, publicKey := range publicKeys {
		cache.expireBlockchain(publicKey, now)
	}
}

// expireBlockchain removes the reasons of the blockchain that have ended at the time now. If no reason is left, it deletes the copy and removes it from the search index.
// It waits for a running update of the copy to end.
func (cache *BlockchainCache) expireBlockchain(publicKey ed25519.PublicKey, now time.Time) {
	cache.peerLock.Lock(string(publicKey))
	defer cache.peerLock.Unlock(string(publicKey))

	// removeEnded removes the reasons that have ended. Reasons that cannot be read are removed.
	removeEnded := func() {
		cache.reasonsMutex.Lock()
		defer cache.reasonsMutex.Unlock()

		reasons, err := cache.Store.ReadReasons(publicKey)
		if err != nil {
			cache.backend.LogError("expireBlockchain", "reading the reasons of blockchain %x: %s\n", publicKey, err.Error())
			reasons = nil
		}

		for reason, existing := range reasons {
			if !existing.Until.After(now) {
				delete(reasons, reason)
			}
		}

		if err := cache.Store.WriteReasons(publicKey, reasons); err != nil {
			cache.backend.LogError("expireBlockchain", "writing the reasons of blockchain %x: %s\n", publicKey, err.Error())
		}
	}

	// isEnded returns whether every reason has ended. Reasons that cannot be read count as ended.
	isEnded := func() bool {
		cache.reasonsMutex.Lock()
		defer cache.reasonsMutex.Unlock()

		reasons, _ := cache.Store.ReadReasons(publicKey)
		for _, existing := range reasons {
			if existing.Until.After(now) {
				return false
			}
		}

		return true
	}

	if !isEnded() {
		removeEnded()
		return
	}

	// The copy is deleted before the reasons are removed. A reason that is set in between stays, and the copy is downloaded again for it.
	if header, found, _ := cache.Store.ReadBlockchainHeader(publicKey); found {
		cache.Store.DeleteBlockchain(header)
		cache.backend.SearchIndex.UnindexBlockchain(publicKey)
	}

	removeEnded()
}

// SeenBlockchainVersion shall be called with information about another peer's blockchain. It updates the copy if a reason is set to keep the blockchain.
// It requests the blocks that the cache does not hold yet, up to the height the peer reports, while the copy and the cache have room for them.
// If the reported version number is newer, all existing blocks are immediately deleted.
func (cache *BlockchainCache) SeenBlockchainVersion(peer *PeerInfo) {
	cache.peerLock.Lock(string(peer.PublicKey))
	defer cache.peerLock.Unlock(string(peer.PublicKey))

	if !cache.isKept(peer.PublicKey) {
		return
	}

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

	if header.Height < height && cache.hasRoom(header) {
		cache.download(peer, header, height)
	}
}

// hasRoom returns whether the copy and the cache have room for a block of the maximum size.
func (cache *BlockchainCache) hasRoom(header *blockchain.MultiBlockchainHeader) bool {
	cache.sizeMutex.Lock()
	defer cache.sizeMutex.Unlock()

	return cache.room(header, 1) > 0
}

// room returns the count of blocks of the maximum size, up to the given count, that the copy and the cache have room for. The caller holds sizeMutex.
func (cache *BlockchainCache) room(header *blockchain.MultiBlockchainHeader, count uint64) uint64 {
	if header.Size >= cache.MaxBlockchainSize {
		return 0
	} else if room := (cache.MaxBlockchainSize - header.Size) / blockchain.MaxBlockSize; count > room {
		count = room
	}

	if cache.MaxSize > 0 {
		used := cache.Store.Size() + cache.sizeReserved

		if used >= cache.MaxSize {
			return 0
		} else if room := (cache.MaxSize - used) / blockchain.MaxBlockSize; count > room {
			count = room
		}
	}

	return count
}

// reserve reserves room in the cache for blocks of the maximum size, up to the given count. It returns the count of blocks it reserved room for.
// The copy has room for them as well. The caller releases the room with release.
func (cache *BlockchainCache) reserve(header *blockchain.MultiBlockchainHeader, count uint64) uint64 {
	cache.sizeMutex.Lock()
	defer cache.sizeMutex.Unlock()

	count = cache.room(header, count)
	cache.sizeReserved += count * blockchain.MaxBlockSize

	return count
}

// release releases the room in the cache that was reserved for the count of blocks.
func (cache *BlockchainCache) release(count uint64) {
	cache.sizeMutex.Lock()
	defer cache.sizeMutex.Unlock()

	cache.sizeReserved -= count * blockchain.MaxBlockSize
}

// download requests the blocks from the height of the header up to the given height from the peer and stores them.
// It waits until fewer blockchains are downloaded than the max count at the same time.
// Each request is for as many blocks as the copy and the cache have room for at the maximum block size; the download ends when there is room for none.
func (cache *BlockchainCache) download(peer *PeerInfo, header *blockchain.MultiBlockchainHeader, height uint64) {
	// With a max count of 0 nothing is downloaded.
	if cap(cache.downloads) == 0 {
		return
	}

	cache.downloads <- struct{}{}
	defer func() { <-cache.downloads }()

	for header.Height < height {
		count := cache.reserve(header, height-header.Height)
		if count == 0 {
			return
		}

		isComplete := cache.downloadBlocks(peer, header, count)
		cache.release(count)

		if !isComplete {
			return
		}
	}
}

// downloadBlocks requests the count of blocks from the height of the header on from the peer and stores them. It returns whether all of them were stored.
// The blocks are stored in the order of their numbers. The download ends at the first block that the peer does not send or that is not valid as next block.
func (cache *BlockchainCache) downloadBlocks(peer *PeerInfo, header *blockchain.MultiBlockchainHeader, count uint64) (isComplete bool) {
	target := protocol.BlockRange{Offset: header.Height, Limit: count}

	conn, _, err := peer.BlockTransferRequest(peer.PublicKey, target.Limit, blockchain.MaxBlockSize, []protocol.BlockRange{target})
	if err != nil {
		return false
	}
	defer conn.Close()

	for height := header.Height + count; header.Height < height; {
		data, _, _, availability, err := protocol.BlockTransferReadBlock(conn, blockchain.MaxBlockSize)
		if err != nil || availability != protocol.GetBlockStatusAvailable {
			return false
		}

		decoded, err := cache.Store.IngestBlock(header, data)
		if err != nil {
			return false
		}

		// index it for search
		cache.backend.SearchIndex.IndexNewBlockDecoded(peer.PublicKey, header.Version, decoded.Number, decoded.RecordsDecoded)
	}

	return true
}

// remoteBlockchainUpdate shall be called for each message of the peer that reports its blockchain version and height.
// It reports the blockchain of the peer to the client if the version or height changed (isUpdate), or if the last report was longer ago than blockchainCacheRetry.
// It starts updating the copy of the blockchain in the blockchain cache if the version or height changed,
// or if the last update was started longer ago than blockchainCacheRetry. One update runs at a time per peer.
// This function is called in the Go routine of the packet worker and therefore must not stall.
func (peer *PeerInfo) remoteBlockchainUpdate(isUpdate bool) {
	cache := peer.Backend.GlobalBlockchainCache
	if cache == nil || peer.BlockchainVersion == 0 && peer.BlockchainHeight == 0 {
		return
	}

	peer.blockchainCacheMutex.Lock()
	peer.blockchainCacheChanged = peer.blockchainCacheChanged || isUpdate
	isReport := isUpdate || time.Since(peer.blockchainCacheReported) >= blockchainCacheRetry
	if isReport {
		peer.blockchainCacheReported = time.Now()
	}
	peer.blockchainCacheMutex.Unlock()

	// The client sets the reasons for which the blockchain is kept before the update starts.
	if isReport {
		peer.Backend.Filters.BlockchainReported(peer)
	}

	peer.blockchainCacheUpdate(false)
}

// blockchainCacheUpdate starts updating the copy of the blockchain of the peer in the blockchain cache if a change of the version or height is noted,
// or if the last update was started longer ago than blockchainCacheRetry. isUpdate notes a change. One update runs at a time per peer.
func (peer *PeerInfo) blockchainCacheUpdate(isUpdate bool) {
	cache := peer.Backend.GlobalBlockchainCache
	if cache == nil || peer.BlockchainVersion == 0 && peer.BlockchainHeight == 0 {
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
