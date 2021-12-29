/*
File Name:  Blockchain User.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"os"

	"github.com/PeernetOfficial/core/blockchain"
)

// initUserBlockchain initializes the users blockchain. It creates the blockchain file if it does not exist already.
// If it is corrupted, it will log the error and exit the process.
func (backend *Backend) initUserBlockchain() {
	var err error
	backend.UserBlockchain, err = blockchain.Init(backend.peerPrivateKey, backend.Config.BlockchainMain)

	if err != nil {
		backend.Filters.LogError("initUserBlockchain", "error: %s\n", err.Error())
		os.Exit(ExitBlockchainCorrupt)
	}
}

// Index the user's blockchain each time there is an update.
func (backend *Backend) userBlockchainUpdateSearchIndex() {
	backend.UserBlockchain.BlockchainUpdate = func(blockchainU *blockchain.Blockchain, oldHeight, oldVersion, newHeight, newVersion uint64) {

		if newVersion != oldVersion || newHeight < oldHeight {
			// invalidate search index data for the user's blockchain
			backend.SearchIndex.UnindexBlockchain(backend.peerPublicKey)

			// reindex everything
			for blockN := uint64(0); blockN < newHeight; blockN++ {
				raw, status, err := blockchainU.GetBlockRaw(blockN)
				if err != nil || status != blockchain.StatusOK {
					continue
				}

				backend.SearchIndex.IndexNewBlock(backend.peerPublicKey, newVersion, blockN, raw)
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

				backend.SearchIndex.IndexNewBlock(backend.peerPublicKey, newVersion, blockN, raw)
			}
		}
	}
}
