/*
File Name:  Blockchain.go
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
