/*
File Name:  Blockchain.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

All blocks and the blockchain header are stored in a key/value database.
The key for the blockchain header is keyHeader and for each block is the block number as 64-bit unsigned integer little endian.

Encoding of the blockchain header:
Offset  Size   Info
0       8      Height of the blockchain
8       8      Version of the blockchain
16      2      Format of the blockchain. This provides backward compatibility.
18      65     Signature

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
	"github.com/google/uuid"
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
	format    uint16
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
		userBlockchainHeader.publicKey = peerPublicKey

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

	if len(buffer) != 83 {
		return nil, 0, 0, true, errors.New("blockchain header size mismatch")
	}

	height = binary.LittleEndian.Uint64(buffer[0:8])
	version = binary.LittleEndian.Uint64(buffer[8:16])
	format := binary.LittleEndian.Uint16(buffer[16:18])
	signature := buffer[18 : 18+65]

	if format != 0 {
		return nil, 0, 0, true, errors.New("future blockchain format not supported. You must go back to the future!")
	}

	publicKey, _, err = btcec.RecoverCompact(btcec.S256(), signature, hashData(buffer[0:18]))

	return
}

// blockchainHeaderWrite writes the header to the blockchain and signs it.
func blockchainHeaderWrite(db store.Store, privateKey *btcec.PrivateKey, height, version uint64) (err error) {
	var buffer [83]byte
	binary.LittleEndian.PutUint64(buffer[0:8], height)
	binary.LittleEndian.PutUint64(buffer[8:16], version)
	binary.LittleEndian.PutUint16(buffer[16:18], 0) // Current format is 0

	signature, err := btcec.SignCompact(btcec.S256(), privateKey, hashData(buffer[0:18]), true)

	if err != nil {
		return err
	} else if len(signature) != 65 {
		return errors.New("signature length invalid")
	}

	copy(buffer[18:18+65], signature)

	err = db.Set([]byte(keyHeader), buffer[:])

	return err
}

// BlockchainStatusX provides information about the blockchain status. Some errors codes indicate a corruption.
const (
	BlockchainStatusOK                 = 0 // No problems in the blockchain detected.
	BlockchainStatusBlockNotFound      = 1 // Missing block in the blockchain.
	BlockchainStatusCorruptBlock       = 2 // Error block encoding
	BlockchainStatusCorruptBlockRecord = 3 // Error block record encoding
	BlockchainStatusDataNotFound       = 4 // Requested data not available in the blockchain
)

// blockNumberToKey returns the database key for the given block number
func blockNumberToKey(number uint64) (key []byte) {
	var target [8]byte
	binary.LittleEndian.PutUint64(target[:], number)

	return target[:]
}

// blockchainIterate iterates over the blockchain. Status is BlockchainStatusX.
// If the callback returns non-zero, the function aborts and returns the inner status code.
func blockchainIterate(callback func(block *Block) int) (status int) {
	// read all blocks until height is reached
	height := userBlockchainHeader.height

	for blockN := uint64(0); blockN < height; blockN++ {
		blockRaw, found := userBlockchainDB.Get(blockNumberToKey(blockN))
		if !found || len(blockRaw) == 0 {
			return BlockchainStatusBlockNotFound
		}

		block, err := decodeBlock(blockRaw)
		if err != nil {
			return BlockchainStatusCorruptBlock
		}

		if statusI := callback(block); statusI != BlockchainStatusOK {
			return statusI
		}
	}

	return BlockchainStatusOK
}

// blockchainIterateDeleteRecord iterates over the blockchain to find records to delete. Status is BlockchainStatusX.
// If the callback returns true, the record will be deleted. The blockchain will be automatically refactored and height and version updated.
func blockchainIterateDeleteRecord(callback func(record *BlockRecordRaw) (delete, corrupt bool)) (newHeight, newVersion uint64, status int) {
	userBlockchainHeader.Lock()
	defer userBlockchainHeader.Unlock()

	// New blockchain keeps track of the new blocks. If anything changes in the blockchain, it must be recalculated and the version number increased.
	var blockchainNew []Block
	refactorBlockchain := false
	refactorVersion := userBlockchainHeader.version + 1

	// Read all blocks until height is reached. At the end the height and version might be different if blocks are deleted.
	height := userBlockchainHeader.height

	for blockN := uint64(0); blockN < height; blockN++ {
		blockRaw, found := userBlockchainDB.Get(blockNumberToKey(blockN))
		if !found || len(blockRaw) == 0 {
			return 0, 0, BlockchainStatusBlockNotFound
		}

		block, err := decodeBlock(blockRaw)
		if err != nil {
			return 0, 0, BlockchainStatusCorruptBlock
		}

		// loop through all records in this block
		refactorBlock := false
		var newRecordsRaw []BlockRecordRaw

		for n := range block.RecordsRaw {
			// delete the block?
			if delete, corrupt := callback(&block.RecordsRaw[n]); corrupt {
				return 0, 0, BlockchainStatusCorruptBlockRecord
			} else if delete {
				refactorBlock = true
				refactorBlockchain = true
			} else {
				newRecordsRaw = append(newRecordsRaw, block.RecordsRaw[n])
			}
		}

		// If refactor, re-calculate the block. All later blocks need to be re-encoded due to change of previous block hash. The version number needs to change.
		if refactorBlock {
			if len(newRecordsRaw) > 0 {
				blockchainNew = append(blockchainNew, Block{OwnerPublicKey: peerPublicKey, RecordsRaw: newRecordsRaw, BlockchainVersion: refactorVersion, Number: uint64(len(blockchainNew))})
			}
		} else {
			blockchainNew = append(blockchainNew, Block{OwnerPublicKey: peerPublicKey, RecordsRaw: block.RecordsRaw, BlockchainVersion: refactorVersion, Number: uint64(len(blockchainNew))})
		}
	}

	if refactorBlockchain {
		var lastBlockHash []byte

		for _, block := range blockchainNew {
			block.LastBlockHash = lastBlockHash

			raw, err := encodeBlock(&block, peerPrivateKey)
			if err != nil {
				return 0, 0, BlockchainStatusCorruptBlock
			}

			// store the block
			userBlockchainDB.Set(blockNumberToKey(block.Number), raw)

			lastBlockHash = hashData(raw)
		}

		userBlockchainHeader.height = uint64(len(blockchainNew))
		userBlockchainHeader.version = refactorVersion

		// update the blockchain header in the database
		blockchainHeaderWrite(userBlockchainDB, peerPrivateKey, userBlockchainHeader.height, userBlockchainHeader.version)

		// delete orphaned blocks
		for n := userBlockchainHeader.height; n < height; n++ {
			userBlockchainDB.Delete(blockNumberToKey(n))
		}
	}

	return userBlockchainHeader.height, userBlockchainHeader.version, BlockchainStatusOK
}

// ---- blockchain manipulation functions ----

// UserBlockchainHeader returns the users blockchain header which stores the height and version number.
func UserBlockchainHeader() (publicKey *btcec.PublicKey, height uint64, version uint64) {
	return userBlockchainHeader.publicKey, userBlockchainHeader.height, userBlockchainHeader.version
}

// UserBlockchainAppend appends a new block to the blockchain based on the provided raw records.
// Status: BlockchainStatusX (0-2): 0 = Success, 1 = Error block not found, 2 = Error block encoding
func UserBlockchainAppend(RecordsRaw []BlockRecordRaw) (newHeight, newVersion uint64, status int) {
	userBlockchainHeader.Lock()
	defer userBlockchainHeader.Unlock()

	block := &Block{OwnerPublicKey: peerPublicKey, RecordsRaw: RecordsRaw}

	// set the last block hash first
	if userBlockchainHeader.height > 0 {
		previousBlockRaw, found := userBlockchainDB.Get(blockNumberToKey(userBlockchainHeader.height - 1))
		if !found || len(previousBlockRaw) == 0 {
			return 0, 0, BlockchainStatusBlockNotFound
		}

		block.LastBlockHash = hashData(previousBlockRaw)
	}

	block.Number = userBlockchainHeader.height
	block.BlockchainVersion = userBlockchainHeader.version

	raw, err := encodeBlock(block, peerPrivateKey)
	if err != nil {
		return 0, 0, BlockchainStatusCorruptBlock
	}

	// increase blockchain height
	userBlockchainHeader.height++

	// store the block
	userBlockchainDB.Set(blockNumberToKey(block.Number), raw)

	// update the blockchain header in the database
	blockchainHeaderWrite(userBlockchainDB, peerPrivateKey, userBlockchainHeader.height, userBlockchainHeader.version)

	return userBlockchainHeader.height, userBlockchainHeader.version, BlockchainStatusOK
}

// UserBlockchainRead reads the block number from the blockchain.
// Status: 0 = Success, 1 = Error block not found, 2 = Error block encoding, 3 = Error block record encoding
// Errors 2 and 3 indicate data corruption.
func UserBlockchainRead(number uint64) (decoded *BlockDecoded, status int, err error) {
	if number >= userBlockchainHeader.height {
		return nil, 1, errors.New("block number exceeds blockchain height")
	}

	blockRaw, found := userBlockchainDB.Get(blockNumberToKey(number))
	if !found || len(blockRaw) == 0 {
		return nil, 1, errors.New("block not found")
	}

	block, err := decodeBlock(blockRaw)
	if err != nil {
		return nil, 2, err
	}

	decoded, err = decodeBlockRecords(block)
	if err != nil {
		return nil, 2, err
	}

	return decoded, 0, nil
}

// UserBlockchainAddFiles adds files to the blockchain
// Status: 0 = Success, 1 = Error previous block not found, 2 = Error block encoding, 3 = Error block record encoding
// It makes sense to group all files in the same directory into one call, since only one directory record will be created per unique directory per block.
func UserBlockchainAddFiles(files []BlockRecordFile) (newHeight, newVersion uint64, status int) {
	encoded, err := encodeBlockRecordFiles(files)
	if err != nil {
		return 0, 0, BlockchainStatusCorruptBlockRecord
	}

	return UserBlockchainAppend(encoded)
}

// UserBlockchainListFiles returns a list of all files. Status is BlockchainStatusX.
// If there is a corruption in the blockchain it will stop reading but return the files parsed so far.
func UserBlockchainListFiles() (files []BlockRecordFile, status int) {
	status = blockchainIterate(func(block *Block) (statusI int) {
		filesMore, err := decodeBlockRecordFiles(block.RecordsRaw)
		if err != nil {
			return BlockchainStatusCorruptBlockRecord
		}
		files = append(files, filesMore...)

		return BlockchainStatusOK
	})

	return files, status
}

// UserProfileReadField reads the specified profile field. See core.ProfileFieldX for the full list. The returned text is UTF-8 text encoded. Status is BlockchainStatusX.
func UserProfileReadField(index uint16) (text string, status int) {
	found := false

	status = blockchainIterate(func(block *Block) (statusI int) {
		profile, err := decodeBlockRecordProfile(block.RecordsRaw)
		if err != nil {
			return BlockchainStatusCorruptBlockRecord
		} else if profile == nil {
			return BlockchainStatusOK
		}

		// Check if the field is available in the profile record. If there are multiple records, only return the latest one.
		for n := range profile.Fields {
			if profile.Fields[n].Type == index {
				text = profile.Fields[n].Text
				found = true
			}
		}

		return BlockchainStatusOK
	})

	if status != BlockchainStatusOK {
		return "", status
	} else if !found {
		return "", BlockchainStatusDataNotFound
	}

	return text, BlockchainStatusOK
}

// UserProfileReadBlob reads a specific profile blob. A blob is binary data. See core.ProfileBlobX. Status is BlockchainStatusX.
func UserProfileReadBlob(index uint16) (blob []byte, status int) {
	found := false

	status = blockchainIterate(func(block *Block) (statusI int) {
		profile, err := decodeBlockRecordProfile(block.RecordsRaw)
		if err != nil {
			return BlockchainStatusCorruptBlockRecord
		} else if profile == nil {
			return BlockchainStatusOK
		}

		// Check if the blob is available in the profile record. If there are multiple records, only return the latest one.
		for n := range profile.Blobs {
			if profile.Blobs[n].Type == index {
				blob = profile.Blobs[n].Data
				found = true
			}
		}

		return BlockchainStatusOK
	})

	if status != BlockchainStatusOK {
		return nil, status
	} else if !found {
		return nil, BlockchainStatusDataNotFound
	}

	return blob, BlockchainStatusOK
}

// UserProfileList lists all profile fields and blobs. Status is BlockchainStatusX.
func UserProfileList() (fields []BlockRecordProfileField, blobs []BlockRecordProfileBlob, status int) {
	uniqueFields := make(map[uint16]string)
	uniqueBlobs := make(map[uint16][]byte)

	status = blockchainIterate(func(block *Block) (statusI int) {
		profile, err := decodeBlockRecordProfile(block.RecordsRaw)
		if err != nil {
			return BlockchainStatusCorruptBlockRecord
		} else if profile == nil {
			return BlockchainStatusOK
		}

		for n := range profile.Fields {
			uniqueFields[profile.Fields[n].Type] = profile.Fields[n].Text
		}

		for n := range profile.Blobs {
			uniqueBlobs[profile.Blobs[n].Type] = profile.Blobs[n].Data
		}

		return BlockchainStatusOK
	})

	for key, value := range uniqueFields {
		fields = append(fields, BlockRecordProfileField{Type: key, Text: value})
	}

	for key, value := range uniqueBlobs {
		blobs = append(blobs, BlockRecordProfileBlob{Type: key, Data: value})
	}

	return fields, blobs, status
}

// UserProfileWrite writes profile fields and blobs to the blockchain
// Status: 0 = Success, 1 = Error previous block not found, 2 = Error block encoding, 3 = Error block record encoding
func UserProfileWrite(profile BlockRecordProfile) (newHeight, newVersion uint64, status int) {
	encoded, err := encodeBlockRecordProfile(profile)
	if err != nil {
		return 0, 0, BlockchainStatusCorruptBlockRecord
	}

	return UserBlockchainAppend(encoded)
}

// UserBlockchainDeleteFiles deletes files from the blockchain. Status is BlockchainStatusX.
func UserBlockchainDeleteFiles(IDs []uuid.UUID) (newHeight, newVersion uint64, status int) {
	return blockchainIterateDeleteRecord(func(record *BlockRecordRaw) (delete, corrupt bool) {
		if record.Type != RecordTypeFile {
			return false, false
		}

		filesDecoded, err := decodeBlockRecordFiles([]BlockRecordRaw{*record})
		if err != nil || len(filesDecoded) != 1 {
			// corruption
			return false, true
		}

		for _, id := range IDs {
			if id == filesDecoded[0].ID { // found a file ID to delete?
				return true, false
			}
		}

		return false, false
	})
}
