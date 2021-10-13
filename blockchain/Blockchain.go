/*
File Name:  Blockchain.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

All blocks and the blockchain header are stored in a key-value database.
The key for the blockchain header is keyHeader and for each block is the block number as 64-bit unsigned integer little endian.

Encoding of the blockchain header:
Offset  Size   Info
0       8      Height of the blockchain
8       8      Version of the blockchain
16      2      Format of the blockchain. This provides backward compatibility.
18      65     Signature

*/

package blockchain

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sync"

	"github.com/PeernetOfficial/core/store"
	"github.com/btcsuite/btcd/btcec"
	"github.com/google/uuid"
)

// Blockchain stores the blockchain's header in memory. Any changes must be synced to disk!
type Blockchain struct {
	// header
	height  uint64 // Height is exchanged as uint32 in the protocol, but stored as uint64.
	version uint64 // Version is always uint64.
	format  uint16 // Format is only locally used.

	// internals
	publicKey  *btcec.PublicKey  // Public Key of the owner. This must match the ones used on disk.
	privateKey *btcec.PrivateKey // Private Key of the owner. This must match the ones used on disk.
	path       string            // Path of the blockchain on disk. Depends on key-value store whether a filename or folder.
	database   store.Store       // The database storing the blockchain.
	sync.Mutex                   // synchronized access to the header
}

// HashFunction must be set by the caller to the hash function (blake3) that shall be used.
var HashFunction func(data []byte) (hash []byte)

// hashSize is the blake3 digest size in bytes
const hashSize = 32

// PublicKey2NodeID must be set by the caller to the function generating the node ID from the public key. Typically it would use the HashFunction.
var PublicKey2NodeID func(publicKey *btcec.PublicKey) (nodeID []byte)

// Init initializes the given blockchain. It creates the blockchain file if it does not exist already.
func Init(privateKey *btcec.PrivateKey, path string) (blockchain *Blockchain, err error) {
	blockchain = &Blockchain{privateKey: privateKey, path: path}
	publicKey := privateKey.PubKey()

	// open existing blockchain file or create new one
	if blockchain.database, err = store.NewPogrebStore(path); err != nil {
		return nil, err
	}

	// verify header
	var found bool

	found, err = blockchain.headerRead()
	if err != nil {
		return blockchain, err // likely corrupt blockchain database
	} else if !found {
		// First run: create header signature!
		blockchain.publicKey = publicKey

		if err := blockchain.headerWrite(0, 0); err != nil {
			return blockchain, err
		}
	} else if !blockchain.publicKey.IsEqual(publicKey) {
		return blockchain, errors.New("corrupt user blockchain database. Public key mismatch")
	}

	return blockchain, nil
}

// the key names in the key-value database are constant and must not collide with block numbers (i.e. they must be >64 bit)
const keyHeader = "header blockchain"

// headerRead reads the header from the blockchain and decodes it.
func (blockchain *Blockchain) headerRead() (found bool, err error) {
	buffer, found := blockchain.database.Get([]byte(keyHeader))
	if !found {
		return false, nil
	}

	if len(buffer) != 83 {
		return true, errors.New("blockchain header size mismatch")
	}

	blockchain.height = binary.LittleEndian.Uint64(buffer[0:8])
	blockchain.version = binary.LittleEndian.Uint64(buffer[8:16])
	blockchain.format = binary.LittleEndian.Uint16(buffer[16:18])
	signature := buffer[18 : 18+65]

	if blockchain.format != 0 {
		return true, errors.New("future blockchain format not supported. You must go back to the future!")
	}

	blockchain.publicKey, _, err = btcec.RecoverCompact(btcec.S256(), signature, HashFunction(buffer[0:18]))

	return
}

// headerWrite writes the header to the blockchain and signs it.
func (blockchain *Blockchain) headerWrite(height, version uint64) (err error) {
	blockchain.height = height
	blockchain.version = version

	var buffer [83]byte
	binary.LittleEndian.PutUint64(buffer[0:8], height)
	binary.LittleEndian.PutUint64(buffer[8:16], version)
	binary.LittleEndian.PutUint16(buffer[16:18], 0) // Current format is 0

	signature, err := btcec.SignCompact(btcec.S256(), blockchain.privateKey, HashFunction(buffer[0:18]), true)

	if err != nil {
		return err
	} else if len(signature) != 65 {
		return errors.New("signature length invalid")
	}

	copy(buffer[18:18+65], signature)

	err = blockchain.database.Set([]byte(keyHeader), buffer[:])

	return err
}

// BlockchainStatusX provides information about the blockchain status. Some errors codes indicate a corruption.
const (
	BlockchainStatusOK                 = 0 // No problems in the blockchain detected.
	BlockchainStatusBlockNotFound      = 1 // Missing block in the blockchain.
	BlockchainStatusCorruptBlock       = 2 // Error block encoding
	BlockchainStatusCorruptBlockRecord = 3 // Error block record encoding
	BlockchainStatusDataNotFound       = 4 // Requested data not available in the blockchain
	BlockchainStatusNotInWarehouse     = 5 // File to be added to blockchain does not exist in the Warehouse
)

// blockNumberToKey returns the database key for the given block number
func blockNumberToKey(number uint64) (key []byte) {
	var target [8]byte
	binary.LittleEndian.PutUint64(target[:], number)

	return target[:]
}

// Iterate iterates over the blockchain. Status is BlockchainStatusX.
// If the callback returns non-zero, the function aborts and returns the inner status code.
func (blockchain *Blockchain) Iterate(callback func(block *Block) int) (status int) {
	// read all blocks until height is reached
	height := blockchain.height

	for blockN := uint64(0); blockN < height; blockN++ {
		blockRaw, found := blockchain.database.Get(blockNumberToKey(blockN))
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

// IterateDeleteRecord iterates over the blockchain to find records to delete. Status is BlockchainStatusX.
// deleteAction is 0 = no action on record, 1 = delete record, 2 = replace record, 3 = error blockchain corrupt
// If the callback returns true, the record will be deleted. The blockchain will be automatically refactored and height and version updated.
func (blockchain *Blockchain) IterateDeleteRecord(callback func(record *BlockRecordRaw) (deleteAction int)) (newHeight, newVersion uint64, status int) {
	blockchain.Lock()
	defer blockchain.Unlock()

	// New blockchain keeps track of the new blocks. If anything changes in the blockchain, it must be recalculated and the version number increased.
	var blockchainNew []Block
	refactorBlockchain := false
	refactorVersion := blockchain.version + 1

	// Read all blocks until height is reached. At the end the height and version might be different if blocks are deleted.
	height := blockchain.height

	for blockN := uint64(0); blockN < height; blockN++ {
		blockRaw, found := blockchain.database.Get(blockNumberToKey(blockN))
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
			switch callback(&block.RecordsRaw[n]) {
			case 0: // no action on record
				newRecordsRaw = append(newRecordsRaw, block.RecordsRaw[n])

			case 1: // delete record
				refactorBlock = true
				refactorBlockchain = true

			case 2: // replace record
				newRecordsRaw = append(newRecordsRaw, block.RecordsRaw[n])
				refactorBlock = true
				refactorBlockchain = true

			case 3: // error blockchain corrupt
				return 0, 0, BlockchainStatusCorruptBlockRecord
			}
		}

		// If refactor, re-calculate the block. All later blocks need to be re-encoded due to change of previous block hash. The version number needs to change.
		if refactorBlock {
			if len(newRecordsRaw) > 0 {
				blockchainNew = append(blockchainNew, Block{OwnerPublicKey: blockchain.publicKey, RecordsRaw: newRecordsRaw, BlockchainVersion: refactorVersion, Number: uint64(len(blockchainNew))})
			}
		} else {
			blockchainNew = append(blockchainNew, Block{OwnerPublicKey: blockchain.publicKey, RecordsRaw: block.RecordsRaw, BlockchainVersion: refactorVersion, Number: uint64(len(blockchainNew))})
		}
	}

	if refactorBlockchain {
		var lastBlockHash []byte

		for _, block := range blockchainNew {
			block.LastBlockHash = lastBlockHash

			raw, err := encodeBlock(&block, blockchain.privateKey)
			if err != nil {
				return 0, 0, BlockchainStatusCorruptBlock
			}

			// store the block
			blockchain.database.Set(blockNumberToKey(block.Number), raw)

			lastBlockHash = HashFunction(raw)
		}

		// update the blockchain header in the database
		blockchain.headerWrite(uint64(len(blockchainNew)), refactorVersion)

		// delete orphaned blocks
		for n := blockchain.height; n < height; n++ {
			blockchain.database.Delete(blockNumberToKey(n))
		}
	}

	return blockchain.height, blockchain.version, BlockchainStatusOK
}

// ---- blockchain manipulation functions ----

// Header returns the users blockchain header which stores the height and version number.
func (blockchain *Blockchain) Header() (publicKey *btcec.PublicKey, height uint64, version uint64) {
	blockchain.Lock()
	defer blockchain.Unlock()

	return blockchain.publicKey, blockchain.height, blockchain.version
}

// Append appends a new block to the blockchain based on the provided raw records.
// Status: BlockchainStatusX (0-2): 0 = Success, 1 = Error block not found, 2 = Error block encoding
func (blockchain *Blockchain) Append(RecordsRaw []BlockRecordRaw) (newHeight, newVersion uint64, status int) {
	blockchain.Lock()
	defer blockchain.Unlock()

	block := &Block{OwnerPublicKey: blockchain.publicKey, RecordsRaw: RecordsRaw}

	// set the last block hash first
	if blockchain.height > 0 {
		previousBlockRaw, found := blockchain.database.Get(blockNumberToKey(blockchain.height - 1))
		if !found || len(previousBlockRaw) == 0 {
			return 0, 0, BlockchainStatusBlockNotFound
		}

		block.LastBlockHash = HashFunction(previousBlockRaw)
	}

	block.Number = blockchain.height
	block.BlockchainVersion = blockchain.version

	raw, err := encodeBlock(block, blockchain.privateKey)
	if err != nil {
		return 0, 0, BlockchainStatusCorruptBlock
	}

	// store the block
	blockchain.database.Set(blockNumberToKey(block.Number), raw)

	// update the blockchain header in the database, increase blockchain height
	blockchain.headerWrite(blockchain.height+1, blockchain.version)

	return blockchain.height, blockchain.version, BlockchainStatusOK
}

// Read reads the block number from the blockchain. Status is BlockchainStatusX.
func (blockchain *Blockchain) Read(number uint64) (decoded *BlockDecoded, status int, err error) {
	if number >= blockchain.height {
		return nil, BlockchainStatusBlockNotFound, errors.New("block number exceeds blockchain height")
	}

	blockRaw, found := blockchain.database.Get(blockNumberToKey(number))
	if !found || len(blockRaw) == 0 {
		return nil, BlockchainStatusBlockNotFound, errors.New("block not found")
	}

	block, err := decodeBlock(blockRaw)
	if err != nil {
		return nil, BlockchainStatusCorruptBlock, err
	}

	decoded, err = decodeBlockRecords(block)
	if err != nil {
		return nil, BlockchainStatusCorruptBlock, err
	}

	return decoded, BlockchainStatusOK, nil
}

// AddFiles adds files to the blockchain. Status is BlockchainStatusX.
// It makes sense to group all files in the same directory into one call, since only one directory record will be created per unique directory per block.
func (blockchain *Blockchain) AddFiles(files []BlockRecordFile) (newHeight, newVersion uint64, status int) {
	encoded, err := encodeBlockRecordFiles(files)
	if err != nil {
		return 0, 0, BlockchainStatusCorruptBlockRecord
	}

	return blockchain.Append(encoded)
}

// ListFiles returns a list of all files. Status is BlockchainStatusX.
// If there is a corruption in the blockchain it will stop reading but return the files parsed so far.
func (blockchain *Blockchain) ListFiles() (files []BlockRecordFile, status int) {
	status = blockchain.Iterate(func(block *Block) (statusI int) {
		filesMore, err := decodeBlockRecordFiles(block.RecordsRaw, block.NodeID)
		if err != nil {
			return BlockchainStatusCorruptBlockRecord
		}
		files = append(files, filesMore...)

		return BlockchainStatusOK
	})

	return files, status
}

// FileExists checks if the file (identified via its hash) exists.
// If there is a corruption in the blockchain it will stop reading but return the files found so far.
func (blockchain *Blockchain) FileExists(hash []byte) (files []BlockRecordFile, status int) {
	status = blockchain.Iterate(func(block *Block) (statusI int) {
		filesD, err := decodeBlockRecordFiles(block.RecordsRaw, block.NodeID)
		if err != nil {
			return BlockchainStatusCorruptBlockRecord
		}
		for _, file := range filesD {
			if bytes.Equal(file.Hash, hash) {
				files = append(files, file)
			}
		}

		return BlockchainStatusOK
	})

	return files, status
}

// ProfileReadField reads the specified profile field. See ProfileX for the list of recognized fields. The encoding depends on the field type. Status is BlockchainStatusX.
func (blockchain *Blockchain) ProfileReadField(index uint16) (data []byte, status int) {
	found := false

	status = blockchain.Iterate(func(block *Block) (statusI int) {
		fields, err := decodeBlockRecordProfile(block.RecordsRaw)
		if err != nil {
			return BlockchainStatusCorruptBlockRecord
		} else if len(fields) == 0 {
			return BlockchainStatusOK
		}

		// Check if the field is available in the profile record. If there are multiple records, only return the latest one.
		for n := range fields {
			if fields[n].Type == index {
				data = fields[n].Data
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

	return data, BlockchainStatusOK
}

// ProfileList lists all profile fields. Status is BlockchainStatusX.
func (blockchain *Blockchain) ProfileList() (fields []BlockRecordProfile, status int) {
	uniqueFields := make(map[uint16][]byte)

	status = blockchain.Iterate(func(block *Block) (statusI int) {
		fields, err := decodeBlockRecordProfile(block.RecordsRaw)
		if err != nil {
			return BlockchainStatusCorruptBlockRecord
		}

		for n := range fields {
			uniqueFields[fields[n].Type] = fields[n].Data
		}

		return BlockchainStatusOK
	})

	for key, value := range uniqueFields {
		fields = append(fields, BlockRecordProfile{Type: key, Data: value})
	}

	return fields, status
}

// ProfileWrite writes profile fields and blobs to the blockchain. Status is BlockchainStatusX.
func (blockchain *Blockchain) ProfileWrite(fields []BlockRecordProfile) (newHeight, newVersion uint64, status int) {
	encoded, err := encodeBlockRecordProfile(fields)
	if err != nil {
		return 0, 0, BlockchainStatusCorruptBlockRecord
	}

	return blockchain.Append(encoded)
}

// ProfileDelete deletes fields and blobs from the blockchain. Status is BlockchainStatusX.
func (blockchain *Blockchain) ProfileDelete(fields []uint16) (newHeight, newVersion uint64, status int) {
	return blockchain.IterateDeleteRecord(func(record *BlockRecordRaw) (deleteAction int) {
		if record.Type != RecordTypeProfile {
			return 0 // no action
		}

		existingFields, err := decodeBlockRecordProfile([]BlockRecordRaw{*record})
		if err != nil || len(existingFields) != 1 {
			return 3 // error blockchain corrupt
		}

		for _, i := range fields {
			if i == existingFields[0].Type { // found a file ID to delete?
				return 1 // delete record
			}
		}

		return 0 // no action on record
	})
}

// DeleteFiles deletes files from the blockchain. Status is BlockchainStatusX.
func (blockchain *Blockchain) DeleteFiles(IDs []uuid.UUID) (newHeight, newVersion uint64, deletedFiles []BlockRecordFile, status int) {
	newHeight, newVersion, status = blockchain.IterateDeleteRecord(func(record *BlockRecordRaw) (deleteAction int) {
		if record.Type != RecordTypeFile {
			return 0 // no action on record
		}

		filesDecoded, err := decodeBlockRecordFiles([]BlockRecordRaw{*record}, nil)
		if err != nil || len(filesDecoded) != 1 {
			return 3 // error blockchain corrupt
		}

		for _, id := range IDs {
			if id == filesDecoded[0].ID { // found a file ID to delete?
				deletedFiles = append(deletedFiles, filesDecoded[0])
				return 1 // delete record
			}
		}

		return 0 // no action on record
	})

	return
}
