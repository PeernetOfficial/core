/*
File Name:  Blockchain.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

All blocks and the blockchain header are stored in a key/value database.
The key for the blockchain header is keyHeader and for each block is the block number as 64-bit unsigned integer little endian.

Encoding of the blockchain header:
Offset  Size   Info
0       8      Height
8       8      Version
16      65     Signature

Encoding of each block (it is the same stored in the database and shared in a message):
Offset  Size   Info
0       65     Signature of entire block
65      32     Hash (blake3) of last block. 0 for first one.
97      8      Blockchain version number
105     4      Block number
109     4      Size of entire block including this header
113     2      Count of records that follow

Each record inside the block has this basic structure:
Offset  Size   Info
0       1      Record Type
1       4      Size of data
5       ?      Data (encoding depends on record type)

*/

package core

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"sync"

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
const keyHeader = "header blockchain"

// userBlockchainHeader stores the users blockchain header in memory. Any changes must be synced to disk!
var userBlockchainHeader struct {
	height    uint64
	version   uint64
	publicKey *btcec.PublicKey
	sync.Mutex
}

var userBlockchainDB store.Store

// initUserBlockchain initializes the users blockchain. It creates the blockchain file if it does not exist already.
// If it is corrupted, it will log the error and exit the process.
func initUserBlockchain() {
	// open existing blockchain file or create new one
	var err error
	if userBlockchainDB, err = store.NewPogrebStore(filenameUserBlockchain); err != nil {
		Filters.LogError("initUserBlockchain", "error opening user blockchain: %s\n", err.Error())
		os.Exit(1)
	}

	// verify header
	var found bool
	userBlockchainHeader.publicKey, userBlockchainHeader.height, userBlockchainHeader.version, found, err = blockchainHeaderRead(userBlockchainDB)
	if err != nil {
		Filters.LogError("initUserBlockchain", "corrupt user blockchain database: %s\n", err.Error())
		os.Exit(1)
	} else if !found {
		// First run: create header signature!
		userBlockchainHeader.height = 0
		userBlockchainHeader.version = 0
		if err := blockchainHeaderWrite(userBlockchainDB, peerPrivateKey, userBlockchainHeader.height, userBlockchainHeader.version); err != nil {
			Filters.LogError("initUserBlockchain", "initializing user blockchain: %s", err.Error())
			os.Exit(1)
		}
	} else if !userBlockchainHeader.publicKey.IsEqual(peerPublicKey) {
		Filters.LogError("initUserBlockchain", "corrupt user blockchain database. Public key mismatch. Height is '%d', version '%d'. Public key expected '%s' vs provided '%s'\n", userBlockchainHeader.height, userBlockchainHeader.version, hex.EncodeToString(peerPublicKey.SerializeCompressed()), hex.EncodeToString(userBlockchainHeader.publicKey.SerializeCompressed()))
		os.Exit(1)
	}
}

// blockchainHeaderRead reads the header from the blockchain and decodes it.
func blockchainHeaderRead(db store.Store) (publicKey *btcec.PublicKey, height, version uint64, found bool, err error) {
	buffer, found := db.Get([]byte(keyHeader))
	if !found {
		return nil, 0, 0, false, nil
	}

	if len(buffer) != 81 {
		return nil, 0, 0, true, errors.New("blockchain header size mismatch")
	}

	height = binary.LittleEndian.Uint64(buffer[0:8])
	version = binary.LittleEndian.Uint64(buffer[8:16])
	signature := buffer[16 : 16+65]

	publicKey, _, err = btcec.RecoverCompact(btcec.S256(), signature, hashData(buffer[0:16]))

	return
}

// blockchainHeaderWrite writes the header to the blockchain and signs it.
func blockchainHeaderWrite(db store.Store, privateKey *btcec.PrivateKey, height, version uint64) (err error) {
	var buffer [81]byte
	binary.LittleEndian.PutUint64(buffer[0:8], height)
	binary.LittleEndian.PutUint64(buffer[8:16], version)

	signature, err := btcec.SignCompact(btcec.S256(), privateKey, hashData(buffer[0:16]), true)

	if err != nil {
		return err
	} else if len(signature) != 65 {
		return errors.New("signature length invalid")
	}

	copy(buffer[16:16+65], signature)

	err = db.Set([]byte(keyHeader), buffer[:])

	return err
}

// UserBlockchainHeader returns the users blockchain header which stores the height and version number.
func UserBlockchainHeader() (publicKey *btcec.PublicKey, height uint64, version uint64) {
	return userBlockchainHeader.publicKey, userBlockchainHeader.height, userBlockchainHeader.version
}

// ---- low-level blockchain manipulation functions ----

// UserBlockchainAppend appends a new block to the blockchain based on the provided raw records.
// Status: 0 = Success, 1 = Error previous block not found, 2 = Error block encoding
func UserBlockchainAppend(RecordsRaw []BlockRecordRaw) (newHeight uint64, status int) {
	userBlockchainHeader.Lock()
	defer userBlockchainHeader.Unlock()

	block := &Block{OwnerPublicKey: peerPublicKey, RecordsRaw: RecordsRaw}

	// set the last block hash first
	if userBlockchainHeader.height > 0 {
		var target [8]byte
		binary.LittleEndian.PutUint64(target[:], userBlockchainHeader.height-1)
		previousBlockRaw, found := userBlockchainDB.Get(target[:])
		if !found || len(previousBlockRaw) == 0 {
			return 0, 1
		}

		block.LastBlockHash = hashData(previousBlockRaw)
	}

	block.Number = userBlockchainHeader.height
	block.BlockchainVersion = userBlockchainHeader.version

	raw, err := encodeBlock(block, peerPrivateKey)
	if err != nil {
		return 0, 2
	}

	// increase blockchain height
	userBlockchainHeader.height++

	// store the block
	var numberB [8]byte
	binary.LittleEndian.PutUint64(numberB[:], block.Number)
	userBlockchainDB.Set(numberB[:], raw)

	// update the blockchain header in the database
	blockchainHeaderWrite(userBlockchainDB, peerPrivateKey, userBlockchainHeader.height, userBlockchainHeader.version)

	return userBlockchainHeader.height, 0
}

// UserBlockchainRead reads the block number from the blockchain.
// Status: 0 = Success, 1 = Error block not found, 2 = Error block encoding, 3 = Error block record encoding
// Errors 2 and 3 indicate data corruption.
func UserBlockchainRead(number uint64) (decoded *BlockDecoded, status int) {
	if number >= userBlockchainHeader.height {
		return nil, 1
	}

	var target [8]byte
	binary.LittleEndian.PutUint64(target[:], userBlockchainHeader.height-1)
	blockRaw, found := userBlockchainDB.Get(target[:])
	if !found || len(blockRaw) == 0 {
		return nil, 1
	}

	block, err := decodeBlock(blockRaw)
	if err != nil {
		return nil, 2
	}

	decoded, err = decodeBlockRecords(block)
	if err != nil {
		return nil, 2
	}

	return decoded, 0
}

// UserBlockchainAddFiles adds files to the blockchain
// Status: 0 = Success, 1 = Error previous block not found, 2 = Error block encoding, 3 = Error block record encoding
// It makes sense to group all files in the same directory into one call, since only one directory record will be created per unique directory per block.
func UserBlockchainAddFiles(files []BlockRecordFile) (newHeight uint64, status int) {
	encoded, err := encodeBlockRecordFiles(files)
	if err != nil {
		return 0, 3
	}

	return UserBlockchainAppend(encoded)
}

// UserBlockchainListFiles returns a list of all files
// If there is a corruption in the blockchain it will reading it but return the files parsed so far.
// Status: 0 = Success, 1 = Block not found, 2 = Error block encoding, 3 = Error block record encoding
func UserBlockchainListFiles() (files []BlockRecordFile, status int) {
	// TODO: Add internal cache of file list for faster subsequent processing?
	height := userBlockchainHeader.height

	// read all blocks until height is reached
	for blockN := uint64(0); blockN < height; blockN++ {
		var target [8]byte
		binary.LittleEndian.PutUint64(target[:], userBlockchainHeader.height-1)
		blockRaw, found := userBlockchainDB.Get(target[:])
		if !found || len(blockRaw) == 0 {
			return files, 1
		}

		block, err := decodeBlock(blockRaw)
		if err != nil {
			return files, 2
		}

		filesMore, _, err := decodeBlockRecordFiles(block.RecordsRaw)
		if err != nil {
			return nil, 3
		}

		files = append(files, filesMore...)
	}

	return files, 0
}
