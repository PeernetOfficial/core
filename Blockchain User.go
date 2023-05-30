/*
File Username:  Blockchain User.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"os"

	"github.com/PeernetOfficial/core/blockchain"
	"github.com/PeernetOfficial/core/btcec"
	"github.com/google/uuid"
)

// initUserBlockchain initializes the users blockchain. It creates the blockchain file if it does not exist already.
// If it is corrupted, it will log the error and exit the process.
func (backend *Backend) initUserBlockchain() {
	var err error
	backend.UserBlockchain, err = blockchain.Init(backend.PeerPrivateKey, backend.Config.BlockchainMain)

	if err != nil {
		backend.LogError("initUserBlockchain", "error: %s\n", err.Error())
		os.Exit(ExitBlockchainCorrupt)
	}
}

// Index the user's blockchain each time there is an update.
func (backend *Backend) userBlockchainUpdateSearchIndex() {
	backend.UserBlockchain.BlockchainUpdate = func(blockchainU *blockchain.Blockchain, oldHeight, oldVersion, newHeight, newVersion uint64) {

		if newVersion != oldVersion || newHeight < oldHeight {
			// invalidate search index data for the user's blockchain
			backend.SearchIndex.UnindexBlockchain(backend.PeerPublicKey)

			// reindex everything
			for blockN := uint64(0); blockN < newHeight; blockN++ {
				raw, status, err := blockchainU.GetBlockRaw(blockN)
				if err != nil || status != blockchain.StatusOK {
					continue
				}

				backend.SearchIndex.IndexNewBlock(backend.PeerPublicKey, newVersion, blockN, raw)
			}

			return
		}

		if newVersion == oldVersion && newHeight > oldHeight {
			// index the new blocks
			for blockN := oldHeight; blockN < newHeight; blockN++ {
				raw, status, err := blockchainU.GetBlockRaw(blockN)
				if err != nil || status != blockchain.StatusOK {
					continue
				}

				backend.SearchIndex.IndexNewBlock(backend.PeerPublicKey, newVersion, blockN, raw)
			}
		}
	}
}

// ReadBlock reads a block and decodes the records. This may be a block of the user's blockchain, or any other that is cached in the global blockchain cache.
func (backend *Backend) ReadBlock(PublicKey *btcec.PublicKey, Version, BlockNumber uint64) (decoded *blockchain.BlockDecoded, raw []byte, found bool, err error) {
	// requesting a block from the user's blockchain?
	if PublicKey.IsEqual(backend.PeerPublicKey) {
		_, _, version := backend.UserBlockchain.Header()
		if Version != version {
			return nil, nil, false, nil
		}

		var status int
		raw, status, err = backend.UserBlockchain.GetBlockRaw(BlockNumber)
		if err != nil || status != blockchain.StatusOK {
			return nil, raw, false, err
		}
	} else if backend.GlobalBlockchainCache != nil {
		// read from the cache
		if raw, found = backend.GlobalBlockchainCache.Store.ReadBlock(PublicKey, Version, BlockNumber); !found {
			return nil, nil, false, nil
		}
	} else {
		return nil, nil, false, nil
	}

	// decode the entire block
	blockDecoded, status, err := blockchain.DecodeBlockRaw(raw)
	if err != nil || status != blockchain.StatusOK {
		return nil, raw, false, err
	}

	return blockDecoded, raw, true, nil
}

// ReadFile decodes a file from the given blockchain (the user or any other cached). The block number must be provided.
func (backend *Backend) ReadFile(PublicKey *btcec.PublicKey, Version, BlockNumber uint64, FileID uuid.UUID) (file blockchain.BlockRecordFile, raw []byte, found bool, err error) {
	blockDecoded, raw, found, err := backend.ReadBlock(PublicKey, Version, BlockNumber)
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
