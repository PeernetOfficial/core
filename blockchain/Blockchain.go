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
	"encoding/binary"
	"errors"
	"sync"

	"github.com/PeernetOfficial/core/btcec"
	"github.com/PeernetOfficial/core/protocol"
	"github.com/PeernetOfficial/core/store"
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

	blockchain.publicKey, _, err = btcec.RecoverCompact(btcec.S256(), signature, protocol.HashData(buffer[0:18]))

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

	signature, err := btcec.SignCompact(btcec.S256(), blockchain.privateKey, protocol.HashData(buffer[0:18]), true)

	if err != nil {
		return err
	} else if len(signature) != 65 {
		return errors.New("signature length invalid")
	}

	copy(buffer[18:18+65], signature)

	err = blockchain.database.Set([]byte(keyHeader), buffer[:])

	return err
}

// StatusX provides information about the blockchain status. Some errors codes indicate a corruption.
const (
	StatusOK                 = 0 // No problems in the blockchain detected.
	StatusBlockNotFound      = 1 // Missing block in the blockchain.
	StatusCorruptBlock       = 2 // Error block encoding
	StatusCorruptBlockRecord = 3 // Error block record encoding
	StatusDataNotFound       = 4 // Requested data not available in the blockchain
	StatusNotInWarehouse     = 5 // File to be added to blockchain does not exist in the Warehouse
)

// blockNumberToKey returns the database key for the given block number
func blockNumberToKey(number uint64) (key []byte) {
	var target [8]byte
	binary.LittleEndian.PutUint64(target[:], number)

	return target[:]
}

// Iterate iterates over the blockchain. Status is StatusX.
// If the callback returns non-zero, the function aborts and returns the inner status code.
func (blockchain *Blockchain) Iterate(callback func(block *Block) int) (status int) {
	// read all blocks until height is reached
	height := blockchain.height

	for blockN := uint64(0); blockN < height; blockN++ {
		blockRaw, found := blockchain.database.Get(blockNumberToKey(blockN))
		if !found || len(blockRaw) == 0 {
			return StatusBlockNotFound
		}

		block, err := decodeBlock(blockRaw)
		if err != nil {
			return StatusCorruptBlock
		}

		if statusI := callback(block); statusI != StatusOK {
			return statusI
		}
	}

	return StatusOK
}

// IterateDeleteRecord iterates over the blockchain to find records to delete. Status is StatusX.
// deleteAction is 0 = no action on record, 1 = delete record, 2 = replace record, 3 = error blockchain corrupt
// If the callback returns true, the record will be deleted. The blockchain will be automatically refactored and height and version updated.
func (blockchain *Blockchain) IterateDeleteRecord(callbackFile func(file *BlockRecordFile) (deleteAction int), callbackOther func(record *BlockRecordRaw) (deleteAction int)) (newHeight, newVersion uint64, status int) {
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
			return 0, 0, StatusBlockNotFound
		}

		block, err := decodeBlock(blockRaw)
		if err != nil {
			return 0, 0, StatusCorruptBlock
		}

		refactorBlock := false

		// Decode all file records at once. This is needed due to potential referenced tags.
		// If a file is deleted or referenced tag data changed, it would corrupt the blockchain if the other records were not updated.
		filesD, err := decodeBlockRecordFiles(block.RecordsRaw, block.NodeID)
		if err != nil {
			return 0, 0, StatusCorruptBlock
		}

		// loop through all file records in this block
		var newFileRecords []BlockRecordFile

		if callbackFile == nil {
			newFileRecords = filesD
		} else {
			for n := range filesD {
				switch callbackFile(&filesD[n]) {
				case 0: // no action on record
					newFileRecords = append(newFileRecords, filesD[n])

				case 1: // delete record
					refactorBlock = true
					refactorBlockchain = true

				case 2: // replace record
					newFileRecords = append(newFileRecords, filesD[n])
					refactorBlock = true
					refactorBlockchain = true

				case 3: // error blockchain corrupt
					return 0, 0, StatusCorruptBlockRecord
				}
			}
		}

		// loop through all other (non-file) records in this block
		var newRecordsRaw []BlockRecordRaw

		for n := range block.RecordsRaw {
			// File and Tag records were already handled in above loop.
			if block.RecordsRaw[n].Type == RecordTypeFile || block.RecordsRaw[n].Type == RecordTypeTagData {
				continue
			}

			if callbackOther == nil {
				newRecordsRaw = append(newRecordsRaw, block.RecordsRaw[n])
			} else {
				switch callbackOther(&block.RecordsRaw[n]) {
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
					return 0, 0, StatusCorruptBlockRecord
				}
			}
		}

		// If refactor, re-calculate the block. All later blocks need to be re-encoded due to change of previous block hash. The version number needs to change.
		// Note: Deleting records may leave referenced records orphaned, such as RecordTypeTagData for deleted file records.
		if refactorBlock {
			// re-encode the block
			filesRecords, err := encodeBlockRecordFiles(newFileRecords)
			if err != nil {
				return 0, 0, StatusCorruptBlock
			}

			newRecordsRaw = append(newRecordsRaw, filesRecords...)

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
				return 0, 0, StatusCorruptBlock
			}

			// store the block
			blockchain.database.Set(blockNumberToKey(block.Number), raw)

			lastBlockHash = protocol.HashData(raw)
		}

		// update the blockchain header in the database
		blockchain.headerWrite(uint64(len(blockchainNew)), refactorVersion)

		// delete orphaned blocks
		for n := blockchain.height; n < height; n++ {
			blockchain.database.Delete(blockNumberToKey(n))
		}
	}

	return blockchain.height, blockchain.version, StatusOK
}

// ---- blockchain manipulation functions ----

// Header returns the users blockchain header which stores the height and version number.
func (blockchain *Blockchain) Header() (publicKey *btcec.PublicKey, height uint64, version uint64) {
	blockchain.Lock()
	defer blockchain.Unlock()

	return blockchain.publicKey, blockchain.height, blockchain.version
}

// Append appends a new block to the blockchain based on the provided raw records. Status is StatusX.
func (blockchain *Blockchain) Append(RecordsRaw []BlockRecordRaw) (newHeight, newVersion uint64, status int) {
	blockchain.Lock()
	defer blockchain.Unlock()

	if len(RecordsRaw) == 0 {
		return blockchain.height, blockchain.version, StatusOK
	}

	block := &Block{OwnerPublicKey: blockchain.publicKey, RecordsRaw: RecordsRaw}

	// set the last block hash first
	if blockchain.height > 0 {
		previousBlockRaw, found := blockchain.database.Get(blockNumberToKey(blockchain.height - 1))
		if !found || len(previousBlockRaw) == 0 {
			return 0, 0, StatusBlockNotFound
		}

		block.LastBlockHash = protocol.HashData(previousBlockRaw)
	}

	block.Number = blockchain.height
	block.BlockchainVersion = blockchain.version

	raw, err := encodeBlock(block, blockchain.privateKey)
	if err != nil {
		return 0, 0, StatusCorruptBlock
	}

	// store the block
	blockchain.database.Set(blockNumberToKey(block.Number), raw)

	// update the blockchain header in the database, increase blockchain height
	blockchain.headerWrite(blockchain.height+1, blockchain.version)

	return blockchain.height, blockchain.version, StatusOK
}

// Read reads the block number from the blockchain. Status is StatusX.
func (blockchain *Blockchain) Read(number uint64) (decoded *BlockDecoded, status int, err error) {
	if number >= blockchain.height {
		return nil, StatusBlockNotFound, errors.New("block number exceeds blockchain height")
	}

	blockRaw, found := blockchain.database.Get(blockNumberToKey(number))
	if !found || len(blockRaw) == 0 {
		return nil, StatusBlockNotFound, errors.New("block not found")
	}

	block, err := decodeBlock(blockRaw)
	if err != nil {
		return nil, StatusCorruptBlock, err
	}

	decoded, err = decodeBlockRecords(block)
	if err != nil {
		return nil, StatusCorruptBlock, err
	}

	return decoded, StatusOK, nil
}

// DeleteBlockchain deletes the entire blockchain
func (blockchain *Blockchain) DeleteBlockchain() (status int, err error) {
	blockchain.Lock()
	defer blockchain.Unlock()

	for n := uint64(0); n < blockchain.height; n++ {
		blockchain.database.Delete(blockNumberToKey(n))
	}

	// update the blockchain header in the database, reset height, increase version
	blockchain.headerWrite(0, blockchain.version+1)

	return StatusOK, nil
}
