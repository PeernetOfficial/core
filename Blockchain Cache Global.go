/*
File Name:  Blockchain Cache Global.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"github.com/PeernetOfficial/core/blockchain"
	"github.com/PeernetOfficial/core/btcec"
	"github.com/PeernetOfficial/core/protocol"
	"github.com/enfipy/locker"
	"github.com/google/uuid"
)

// The blockchain cache stores blockchains.
type BlockchainCache struct {
	BlockchainDirectory string // The directory for storing blockchains in a key-value store.
	MaxBlockSize        uint64 // Max block size to accept.
	MaxBlockCount       uint64 // Max block count to cache per peer.
	LimitTotalRecords   uint64 // Max count of blocks and header in total to keep across all blockchains. 0 = unlimited. Max Records * Max Block Size = Size Limit.
	ReadOnly            bool   // Whether the cache is read only.

	Store    *blockchain.MultiStore
	peerLock *locker.Locker

	backend *Backend
}

func (backend *Backend) initBlockchainCache() {
	if backend.Config.BlockchainGlobal == "" {
		return
	}

	backend.GlobalBlockchainCache = &BlockchainCache{
		backend:             backend,
		BlockchainDirectory: backend.Config.BlockchainGlobal,
		MaxBlockSize:        backend.Config.CacheMaxBlockSize,
		MaxBlockCount:       backend.Config.CacheMaxBlockCount,
		LimitTotalRecords:   backend.Config.LimitTotalRecords,
	}

	var err error
	backend.GlobalBlockchainCache.Store, err = blockchain.InitMultiStore(backend.Config.BlockchainGlobal)
	if err != nil {
		backend.Filters.LogError("initBlockchainCache", "initializing database '%s': %s", backend.Config.BlockchainGlobal, err.Error())
		return
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
// If the reported version number is newer, all existing blocks are immediately deleted.
func (cache *BlockchainCache) SeenBlockchainVersion(peer *PeerInfo) {
	cache.peerLock.Lock(string(peer.PublicKey.SerializeCompressed()))
	defer cache.peerLock.Unlock(string(peer.PublicKey.SerializeCompressed()))

	// intermediate function to download and process blocks
	downloadAndProcessBlocks := func(peer *PeerInfo, header *blockchain.MultiBlockchainHeader, offset, limit uint64) {
		if limit > cache.MaxBlockCount {
			limit = cache.MaxBlockCount
		}

		peer.BlockDownload(peer.PublicKey, cache.MaxBlockCount, cache.MaxBlockSize, []protocol.BlockRange{{Offset: offset, Limit: limit}}, func(data []byte, targetBlock protocol.BlockRange, blockSize uint64, availability uint8) {
			if availability != protocol.GetBlockStatusAvailable {
				return
			}

			if decoded, _ := cache.Store.IngestBlock(header, targetBlock.Offset, data, true); decoded != nil {
				// index it for search
				cache.backend.SearchIndex.IndexNewBlockDecoded(peer.PublicKey, peer.BlockchainVersion, targetBlock.Offset, decoded.RecordsDecoded)
			}
		})
	}

	// get the old header
	header, status, err := cache.Store.AssessBlockchainHeader(peer.PublicKey, peer.BlockchainVersion, peer.BlockchainHeight)
	if err != nil {
		return
	}

	switch status {
	case blockchain.MultiStatusEqual:
		return

	case blockchain.MultiStatusInvalidRemote:
		cache.Store.DeleteBlockchain(header)

		cache.backend.SearchIndex.UnindexBlockchain(peer.PublicKey)

	case blockchain.MultiStatusHeaderNA:
		if header, err = cache.Store.NewBlockchainHeader(peer.PublicKey, peer.BlockchainVersion, peer.BlockchainHeight); err != nil {
			return
		}

		downloadAndProcessBlocks(peer, header, 0, peer.BlockchainHeight)

	case blockchain.MultiStatusNewVersion:
		// delete existing data first, then create it new
		cache.Store.DeleteBlockchain(header)

		cache.backend.SearchIndex.UnindexBlockchain(peer.PublicKey)

		if header, err = cache.Store.NewBlockchainHeader(peer.PublicKey, peer.BlockchainVersion, peer.BlockchainHeight); err != nil {
			return
		}

		downloadAndProcessBlocks(peer, header, 0, peer.BlockchainHeight)

	case blockchain.MultiStatusNewBlocks:
		offset := header.Height
		limit := peer.BlockchainHeight - header.Height

		header.Height = peer.BlockchainHeight

		downloadAndProcessBlocks(peer, header, offset, limit)

	}

	if cache.LimitTotalRecords > 0 {
		// Bug: This code is currently never reached if ReadOnly is true.
		cache.ReadOnly = cache.Store.Database.Count() >= cache.LimitTotalRecords
	}
}

// remoteBlockchainUpdate shall be called to indicate a potential update of the remotes blockchain.
// It will use the blockchain version and height to update the data lake as appropriate.
// This function is called in the Go routine of the packet worker and therefore must not stall.
func (peer *PeerInfo) remoteBlockchainUpdate() {
	if peer.Backend.GlobalBlockchainCache == nil || peer.Backend.GlobalBlockchainCache.ReadOnly || peer.BlockchainVersion == 0 && peer.BlockchainHeight == 0 {
		return
	}

	// TODO: This entire function should be instead a non-blocking message via a buffer channel.
	go peer.Backend.GlobalBlockchainCache.SeenBlockchainVersion(peer)
}

func (cache *BlockchainCache) ReadFile(PublicKey *btcec.PublicKey, Version, BlockNumber uint64, FileID uuid.UUID) (file blockchain.BlockRecordFile, raw []byte, found bool, err error) {
	blockDecoded, raw, found, err := cache.ReadBlock(PublicKey, Version, BlockNumber)
	if !found {
		return file, raw, found, err
	}

	for _, decodedR := range blockDecoded.RecordsDecoded {
		if file, ok := decodedR.(blockchain.BlockRecordFile); ok && file.ID == FileID {
			return file, raw, true, nil
		}
	}

	return file, raw, false, nil
}

// ReadBlock reads a block and decodes the records.
func (cache *BlockchainCache) ReadBlock(PublicKey *btcec.PublicKey, Version, BlockNumber uint64) (decoded *blockchain.BlockDecoded, raw []byte, found bool, err error) {
	// requesting a block from the user's blockchain?
	if PublicKey.IsEqual(cache.backend.peerPublicKey) {
		_, _, version := cache.backend.UserBlockchain.Header()
		if Version != version {
			return nil, nil, false, nil
		}

		var status int
		raw, status, err = cache.backend.UserBlockchain.GetBlockRaw(BlockNumber)
		if err != nil || status != blockchain.StatusOK {
			return nil, raw, false, err
		}
	} else {
		// read from the cache
		if raw, found = cache.Store.ReadBlock(PublicKey, Version, BlockNumber); !found {
			return nil, nil, false, nil
		}
	}

	// decode the entire block
	blockDecoded, status, err := blockchain.DecodeBlockRaw(raw)
	if err != nil || status != blockchain.StatusOK {
		return nil, raw, false, err
	}

	return blockDecoded, raw, true, nil
}
