/*
File Name:  Multi.go
Copyright:  2021 Kleissner Ventures Ltd
Author:     Peter Kleissner

Multi-blockchain store implementation.

Keys used in the key-value store:
1. Key: Public key, Value: Header
2. Key: Public key + version + block number, Value: Block
3. Key: "format", Value: Format of the store
4. Key: Public key + "reasons", Value: Reasons for which the blockchain is kept

*/

package blockchain

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"sort"
	"time"

	"github.com/PeernetOfficial/core/protocol"
	"github.com/PeernetOfficial/core/store"
)

// MaxBlockSize is the maximum size of a block in bytes, including the header and signature.
const MaxBlockSize = 32768

// multiFormat is the format of the multi store. A store in another format is emptied when it is opened.
const multiFormat = 1

// multiFormatKey is the key of the record that holds the format of the store.
var multiFormatKey = []byte("format")

// multiReasonsSuffix follows the public key in the key of the record that holds the reasons for which a blockchain is kept.
var multiReasonsSuffix = []byte("reasons")

// MultiStore stores multiple blockchains.
type MultiStore struct {
	path     string      // Path of the blockchain on disk. Depends on key-value store whether a filename or folder.
	Database store.Store // The database storing the blockchain.

	// callbacks
	FilterStatisticUpdate  func(multi *MultiStore, header *MultiBlockchainHeader, statsOld BlockchainStats)
	FilterBlockchainDelete func(multi *MultiStore, header *MultiBlockchainHeader)
}

// InitMultiStore opens the multi store at the path, or creates a new one. See InitMultiStoreDatabase.
func InitMultiStore(path string) (multi *MultiStore, deleted []ed25519.PublicKey, err error) {
	// open existing blockchain file or create new one
	database, err := store.NewPogrebStore(path)
	if err != nil {
		return nil, nil, err
	}

	if multi, deleted, err = InitMultiStoreDatabase(database); err != nil {
		return nil, nil, err
	}
	multi.path = path

	return multi, deleted, nil
}

// InitMultiStoreDatabase initializes the multi store on the database.
// A database in another format is emptied. The public keys of the blockchains that were deleted that way are returned.
func InitMultiStoreDatabase(database store.Store) (multi *MultiStore, deleted []ed25519.PublicKey, err error) {
	multi = &MultiStore{Database: database}

	if format, found := database.Get(multiFormatKey); found && len(format) == 1 && format[0] == multiFormat {
		return multi, nil, nil
	}

	var keys [][]byte
	database.Iterate(func(key, value []byte) {
		keys = append(keys, bytes.Clone(key))
	})

	for _, key := range keys {
		database.Delete(key)

		if len(key) == ed25519.PublicKeySize {
			deleted = append(deleted, ed25519.PublicKey(key))
		}
	}

	if err = database.Set(multiFormatKey, []byte{multiFormat}); err != nil {
		return nil, nil, err
	}

	return multi, deleted, nil
}

/*
Header for blockchains:

Offset  Size   Info
0       8      Version of the blockchain
8       8      Height: Count of blocks stored. The blocks stored are block 0 to height-1.
16      8      Date first block added
24      8      Date last block added
32      8      Stats: Count of file records
40      8      Stats: Size of all files combined

*/

const multiBlockchainHeaderSize = 48

// This is a header for a single blockchain stored in a multi store.
type MultiBlockchainHeader struct {
	PublicKey           ed25519.PublicKey // Public Key of the blockchain
	Height              uint64            // Count of blocks stored. The blocks stored are block 0 to Height-1. Height is exchanged as uint32 in the protocol, but stored as uint64.
	Version             uint64            // Version is always uint64.
	DateFirstBlockAdded time.Time         // Date the first block was added
	DateLastBlockAdded  time.Time         // Date the last block was added
	Stats               BlockchainStats   // Statistics about the blockchain
}

// Keep statistics about blocks stored for a blockchain
type BlockchainStats struct {
	CountFileRecords uint64 // Count of file records in stored blocks
	SizeAllFiles     uint64 // Size of all files combined in stored blocks
}

func decodeBlockchainHeader(publicKey ed25519.PublicKey, buffer []byte) (header *MultiBlockchainHeader, err error) {
	if len(buffer) != multiBlockchainHeaderSize {
		return nil, errors.New("invalid header length")
	}

	header = &MultiBlockchainHeader{PublicKey: publicKey}
	header.Version = binary.LittleEndian.Uint64(buffer[0:8])
	header.Height = binary.LittleEndian.Uint64(buffer[8:16])
	header.DateFirstBlockAdded = time.Unix(int64(binary.LittleEndian.Uint64(buffer[16:24])), 0)
	header.DateLastBlockAdded = time.Unix(int64(binary.LittleEndian.Uint64(buffer[24:32])), 0)
	header.Stats.CountFileRecords = binary.LittleEndian.Uint64(buffer[32:40])
	header.Stats.SizeAllFiles = binary.LittleEndian.Uint64(buffer[40:48])

	return header, nil
}

// Reads a blockchains header if available.
func (multi *MultiStore) ReadBlockchainHeader(publicKey ed25519.PublicKey) (header *MultiBlockchainHeader, found bool, err error) {
	buffer, found := multi.Database.Get(publicKey)
	if !found {
		return nil, false, nil
	}

	header, err = decodeBlockchainHeader(publicKey, buffer)
	return header, err == nil, err
}

// WriteBlockchainHeader writes a blockchain header. If one exists, it will be overwritten.
func (multi *MultiStore) WriteBlockchainHeader(header *MultiBlockchainHeader) (err error) {
	raw := make([]byte, multiBlockchainHeaderSize)

	binary.LittleEndian.PutUint64(raw[0:8], header.Version)
	binary.LittleEndian.PutUint64(raw[8:16], header.Height)
	binary.LittleEndian.PutUint64(raw[16:24], uint64(header.DateFirstBlockAdded.UTC().Unix()))
	binary.LittleEndian.PutUint64(raw[24:32], uint64(header.DateLastBlockAdded.UTC().Unix()))
	binary.LittleEndian.PutUint64(raw[32:40], header.Stats.CountFileRecords)
	binary.LittleEndian.PutUint64(raw[40:48], header.Stats.SizeAllFiles)

	return multi.Database.Set(header.PublicKey, raw)
}

func lookupKeyForBlock(publicKey ed25519.PublicKey, version, blockNumber uint64) (key []byte) {
	var buffer [16]byte
	binary.LittleEndian.PutUint64(buffer[0:8], version)
	binary.LittleEndian.PutUint64(buffer[8:16], blockNumber)

	key = append(key, publicKey...)
	key = append(key, buffer[:]...)

	return key
}

// ReadBlock reads a raw block
func (multi *MultiStore) ReadBlock(publicKey ed25519.PublicKey, version, blockNumber uint64) (raw []byte, found bool) {
	return multi.Database.Get(lookupKeyForBlock(publicKey, version, blockNumber))
}

// WriteBlock writes a raw block. It does not update the blockchain header.
func (multi *MultiStore) WriteBlock(publicKey ed25519.PublicKey, version, blockNumber uint64, raw []byte) (err error) {
	return multi.Database.Set(lookupKeyForBlock(publicKey, version, blockNumber), raw)
}

// Deletes an entire blockchain from the store. It will delete each block individually and then the header.
func (multi *MultiStore) DeleteBlockchain(header *MultiBlockchainHeader) {
	// first delete all blocks
	for blockN := uint64(0); blockN < header.Height; blockN++ {
		multi.Database.Delete(lookupKeyForBlock(header.PublicKey, header.Version, blockN))
	}

	// delete the header
	multi.Database.Delete(header.PublicKey)

	if multi.FilterBlockchainDelete != nil {
		multi.FilterBlockchainDelete(multi, header)
	}
}

/*
Reasons for which a blockchain is kept, one entry per reason:

Offset  Size   Info
0       1      Reason
1       8      Date the reason was set
9       8      Date the reason ends

*/

const multiReasonSize = 17

// MultiReason is a reason for which a blockchain is kept in a multi store.
type MultiReason struct {
	Since time.Time // Date the reason was set
	Until time.Time // Date the reason ends
}

func lookupKeyForReasons(publicKey ed25519.PublicKey) (key []byte) {
	key = append(key, publicKey...)
	key = append(key, multiReasonsSuffix...)

	return key
}

// ReadReasons reads the reasons for which the blockchain is kept. The key of the map is the reason. The map is empty if there is none.
func (multi *MultiStore) ReadReasons(publicKey ed25519.PublicKey) (reasons map[uint8]MultiReason, err error) {
	buffer, _ := multi.Database.Get(lookupKeyForReasons(publicKey))
	if len(buffer)%multiReasonSize != 0 {
		return nil, errors.New("invalid reasons length")
	}

	reasons = make(map[uint8]MultiReason)

	for ; len(buffer) > 0; buffer = buffer[multiReasonSize:] {
		reasons[buffer[0]] = MultiReason{
			Since: time.Unix(int64(binary.LittleEndian.Uint64(buffer[1:9])), 0),
			Until: time.Unix(int64(binary.LittleEndian.Uint64(buffer[9:17])), 0),
		}
	}

	return reasons, nil
}

// WriteReasons writes the reasons for which the blockchain is kept. The key of the map is the reason. If there is none, the record is deleted.
func (multi *MultiStore) WriteReasons(publicKey ed25519.PublicKey, reasons map[uint8]MultiReason) (err error) {
	if len(reasons) == 0 {
		multi.Database.Delete(lookupKeyForReasons(publicKey))
		return nil
	}

	var numbers []int
	for reason := range reasons {
		numbers = append(numbers, int(reason))
	}
	sort.Ints(numbers)

	raw := make([]byte, 0, len(numbers)*multiReasonSize)

	for _, number := range numbers {
		reason := reasons[uint8(number)]

		raw = append(raw, uint8(number))
		raw = binary.LittleEndian.AppendUint64(raw, uint64(reason.Since.UTC().Unix()))
		raw = binary.LittleEndian.AppendUint64(raw, uint64(reason.Until.UTC().Unix()))
	}

	return multi.Database.Set(lookupKeyForReasons(publicKey), raw)
}

// IterateReasons iterates over the blockchains that reasons are stored for.
func (multi *MultiStore) IterateReasons(callback func(publicKey ed25519.PublicKey)) {
	multi.Database.Iterate(func(key, value []byte) {
		if len(key) == ed25519.PublicKeySize+len(multiReasonsSuffix) && bytes.Equal(key[ed25519.PublicKeySize:], multiReasonsSuffix) {
			callback(bytes.Clone(key[:ed25519.PublicKeySize]))
		}
	})
}

// NewBlockchainHeader returns the header of a blockchain of which no block is stored. The header is written with the first block that is ingested.
func NewBlockchainHeader(publicKey ed25519.PublicKey, version uint64) (header *MultiBlockchainHeader) {
	return &MultiBlockchainHeader{PublicKey: publicKey, Version: version}
}

// Updates the statistics fields of the blockchain header based on the new decoded block records.
// It does not write the blockchain header; multi.WriteBlockchainHeader must called to store the changes.
// The caller must make sure not to call this function on records already processed.
func (multi *MultiStore) UpdateBlockchainStatistics(header *MultiBlockchainHeader, recordsDecoded []interface{}) {
	updatedStats := false
	statsOld := header.Stats

	for _, decodedR := range recordsDecoded {
		if file, ok := decodedR.(BlockRecordFile); ok {
			header.Stats.SizeAllFiles += file.Size
			header.Stats.CountFileRecords++

			updatedStats = true
		}
	}

	if updatedStats && multi.FilterStatisticUpdate != nil {
		multi.FilterStatisticUpdate(multi, header, statsOld)
	}
}

// Iterates over all blockchains stored in the cache
func (multi *MultiStore) IterateBlockchains(callback func(header *MultiBlockchainHeader)) {
	multi.Database.Iterate(func(key, value []byte) {
		if len(key) == ed25519.PublicKeySize {
			// Length 32 indicates key = Public key, value = Header
			blockchainPublicKey := make(ed25519.PublicKey, ed25519.PublicKeySize)
			copy(blockchainPublicKey, key)
			if header, err := decodeBlockchainHeader(blockchainPublicKey, value); err == nil {
				callback(header)
			}
		}
	})
}

// IngestBlock ingests the next block of the blockchain into the store and updates the blockchain header including the statistics.
// The block must be signed by the owner of the blockchain, be of the version of the header, have the number that equals the height of the header,
// and contain the hash of the block before it.
func (multi *MultiStore) IngestBlock(header *MultiBlockchainHeader, raw []byte) (decoded *BlockDecoded, err error) {
	if len(raw) > MaxBlockSize {
		return nil, errors.New("block exceeds the maximum size")
	}

	decoded, _, err = DecodeBlockRaw(raw, header.PublicKey)
	if err != nil {
		return nil, err
	} else if decoded.BlockchainVersion != header.Version {
		return nil, errors.New("block is of another blockchain version")
	} else if decoded.Number != header.Height {
		return nil, errors.New("block is not the next block")
	}

	// The first block contains 0 as hash.
	lastBlockHash := make([]byte, protocol.HashSize)
	if header.Height > 0 {
		lastBlock, found := multi.ReadBlock(header.PublicKey, header.Version, header.Height-1)
		if !found {
			return nil, errors.New("block before it is not stored")
		}
		lastBlockHash = protocol.HashData(lastBlock)
	}
	if !bytes.Equal(decoded.LastBlockHash, lastBlockHash) {
		return nil, errors.New("block does not contain the hash of the block before it")
	}

	if err = multi.WriteBlock(header.PublicKey, header.Version, header.Height, raw); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if header.Height == 0 {
		header.DateFirstBlockAdded = now
	}
	header.DateLastBlockAdded = now
	header.Height++

	multi.UpdateBlockchainStatistics(header, decoded.RecordsDecoded)

	return decoded, multi.WriteBlockchainHeader(header)
}
