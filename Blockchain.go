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

// filenameUserBlockchain is the filename/folder of the user's blockchain
const filenameUserBlockchain = "self.blockchain"

// initUserBlockchain initializes the users blockchain. It creates the blockchain file if it does not exist already.
// If it is corrupted, it will log the error and exit the process.
func initUserBlockchain() {
	blockchain.HashFunction = hashData
	blockchain.PublicKey2NodeID = PublicKey2NodeID

	var err error
	UserBlockchain, err = blockchain.Init(peerPrivateKey, filenameUserBlockchain)

	if err != nil {
		Filters.LogError("initUserBlockchain", "error: %s\n", err.Error())
		os.Exit(1)
	}
}
