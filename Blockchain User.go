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

// UserBlockchain is the user's blockchain and exports functions to directly read and write it
var UserBlockchain *blockchain.Blockchain

// initUserBlockchain initializes the users blockchain. It creates the blockchain file if it does not exist already.
// If it is corrupted, it will log the error and exit the process.
func initUserBlockchain() {
	var err error
	UserBlockchain, err = blockchain.Init(peerPrivateKey, config.BlockchainMain)

	if err != nil {
		Filters.LogError("initUserBlockchain", "error: %s\n", err.Error())
		os.Exit(ExitBlockchainCorrupt)
	}
}

// Index the user's blockchain each time there is an update.
func (backend *Backend) userBlockchainUpdateSearchIndex() {
	UserBlockchain.BlockchainUpdate = func(blockchainU *blockchain.Blockchain, oldHeight, oldVersion, newHeight, newVersion uint64) {

		if newVersion != oldVersion || newHeight < oldHeight {
			// invalidate search index data for the user's blockchain
			backend.SearchIndex.UnindexBlockchain(peerPublicKey)

			// reindex everything
			for blockN := uint64(0); blockN < newHeight; blockN++ {
				raw, status, err := blockchainU.GetBlockRaw(blockN)
				if err != nil || status != blockchain.StatusOK {
					continue
				}

				backend.SearchIndex.IndexNewBlock(peerPublicKey, newVersion, blockN, raw)
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

				backend.SearchIndex.IndexNewBlock(peerPublicKey, newVersion, blockN, raw)
			}
		}
	}
}
