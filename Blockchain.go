/*
File Name:  Blockchain.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"encoding/binary"
	"encoding/hex"
	"os"
	"strconv"

	"github.com/PeernetOfficial/core/store"
	"github.com/btcsuite/btcd/btcec"
)

// BlockchainHeight is the current count of blocks
var BlockchainHeight = uint32(0)

// BlockchainVersion is the version of the blockchain
var BlockchainVersion = uint64(0)

// filenameUserBlockchain is the filename/folder of the user's blockchain
const filenameUserBlockchain = "self.blockchain"

// the key names in the key/value database are constant and must not collide with block numbers (i.e. they must be >64 bit)
const keyHeaderHeight = "header.height"
const keyHeaderVersion = "header.version"
const keyHeaderSignature = "header.signature"

// BlockchainHeader is the virtual blockchain header. The data is not stored as single header record, but split up.
type BlockchainHeader struct {
	Height    uint64
	Version   uint64
	PublicKey *btcec.PublicKey
}

var userBlockchainDB store.Store
var userBlockchainHeader BlockchainHeader

// initUserBlockchain initializes the users blockchain. It creates the blockchain file if it does not exist already.
// If it is corrupted, it will log the error and exit the process.
func initUserBlockchain() {
	// open existing blockchain file or create new one
	blockchain, err := store.NewPebbleStore(filenameUserBlockchain)
	if err != nil {
		Filters.LogError("initUserBlockchain", "error opening user blockchain: %s\n", err.Error())
		os.Exit(1)
	}

	// verify header
	var height, version uint64
	heightB, found1 := blockchain.Get([]byte(keyHeaderHeight))
	versionB, found2 := blockchain.Get([]byte(keyHeaderVersion))
	headerSignature, isSignature := blockchain.Get([]byte(keyHeaderSignature))

	if found1 && len(heightB) != 8 || found2 && len(versionB) != 8 || isSignature && len(headerSignature) != 65 {
		Filters.LogError("initUserBlockchain", "corrupt user blockchain database. Invalid header length. Height is '%s', version '%s' and signature '%s'.\n", hex.EncodeToString(heightB), hex.EncodeToString(versionB), hex.EncodeToString(headerSignature))
		os.Exit(1)
	}

	if found1 && len(heightB) == 8 {
		height = binary.BigEndian.Uint64(heightB)
	}
	if found2 && len(heightB) == 8 {
		version = binary.BigEndian.Uint64(versionB)
	}

	if isSignature {
		// validate header signature
		headerA := strconv.FormatUint(height, 10) + "/" + strconv.FormatUint(version, 10)
		headerPublicKey, _, err := btcec.RecoverCompact(btcec.S256(), headerSignature[:], hashData([]byte(headerA)))
		if err != nil {
			Filters.LogError("initUserBlockchain", "corrupt user blockchain database. Error decoding signature. Height is '%s', version '%s', signature '%s'. %s\n", hex.EncodeToString(heightB), hex.EncodeToString(versionB), hex.EncodeToString(headerSignature), err.Error())
			os.Exit(1)
		}
		if !headerPublicKey.IsEqual(peerPublicKey) {
			Filters.LogError("initUserBlockchain", "corrupt user blockchain database. Signature key mismatch. Height is '%s', version '%s', signature '%s'.\n", hex.EncodeToString(heightB), hex.EncodeToString(versionB), hex.EncodeToString(headerSignature))
			os.Exit(1)
		}

		userBlockchainHeader = BlockchainHeader{Height: height, Version: version, PublicKey: headerPublicKey}

	} else if !found1 && !found2 && !isSignature {
		// First run: create header signature!
		height = 0
		version = 0
		signature, _ := blockchainCreateHeaderSignature(height, version)
		if err := blockchain.Set([]byte(keyHeaderSignature), signature); err != nil {
			Filters.LogError("initUserBlockchain", "initializing user blockchain: %s", err.Error())
			os.Exit(1)
		}

	} else {
		// header signature not present, but height/version is -> corrupt data.
		Filters.LogError("initUserBlockchain", "corrupt user blockchain database. Height is '%s', version '%s' and signature '%s'.\n", hex.EncodeToString(heightB), hex.EncodeToString(versionB), hex.EncodeToString(headerSignature))
		os.Exit(1)
	}

	userBlockchainDB = blockchain
}

func blockchainCreateHeaderSignature(height, version uint64) (signature []byte, err error) {
	headerA := strconv.FormatUint(height, 10) + "/" + strconv.FormatUint(version, 10)
	signature, err = btcec.SignCompact(btcec.S256(), peerPrivateKey, hashData([]byte(headerA)), true)

	if err != nil {
		Filters.LogError("blockchainCreateHeaderSignature", "creating signature of '%s': %s\n", headerA, err.Error())
	}

	return signature, err
}

// UserBlockchainHeader returns the users blockchain header which stores the height and version number.
func UserBlockchainHeader() (header BlockchainHeader) {
	return userBlockchainHeader
}

// ---- low-level blockchain manipulation functions ----
