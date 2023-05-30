/*
File Username:  Multi.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Multi-blockchain store implementation.

Keys used in the key-value store:
1. Key: Public key compressed, Value: Header
2. Key: Public key compressed + version + block number, Value: Block

*/

package blockchain

import (
	"encoding/binary"
	"errors"
	"time"

	"github.com/PeernetOfficial/core/btcec"
	"github.com/PeernetOfficial/core/store"
)

const (
	MultiStatusOK              = 0 // Success.
	MultiStatusErrorReadHeader = 1 // Error reading header for blockchain.
	MultiStatusHeaderNA        = 2 // Header not available. This indicates nothing is stored for the blockchain.
	MultiStatusInvalidRemote   = 3 // Invalid remote reported blockchain version and height. (local cache is "newer" which should not happen)
	MultiStatusEqual           = 4 // Remote blockchain and local cache are equal.
	MultiStatusNewVersion      = 5 // Local cache is out of date. A new version of the blockchain is available.
	MultiStatusNewBlocks       = 6 // Local cache is out of date. Additional blocks are available.
)

// MultiStore stores multiple blockchains.
type MultiStore struct {
	path     string      // Path of the blockchain on disk. Depends on key-value store whether a filename or folder.
	Database store.Store // The database storing the blockchain.

	// callbacks
	FilterStatisticUpdate  func(multi *MultiStore, header *MultiBlockchainHeader, statsOld BlockchainStats)
	FilterBlockchainDelete func(multi *MultiStore, header *MultiBlockchainHeader)
}

func InitMultiStore(path string) (multi *MultiStore, err error) {
	multi = &MultiStore{path: path}

	// open existing blockchain file or create new one
	if multi.Database, err = store.NewPogrebStore(path); err != nil {
		return nil, err
	}

	return multi, nil
}

/*
Header for blockchains:

Offset  Size   Info
0       8      Version of the blockchain
8       8      Height of the blockchain
16      8      Count of blocks
24      8      Date first block added
32      8      Date last block added
40      8      Stats: Count of file records  (from available blocks)
48      8      Stats: Size of all files combined (from available blocks)
56      8 * n  List of block numbers that are stored

Note: The statistics fields only count available stored blocks.

*/

const multiBlockchainHeaderSize = 56

// This is a header for a single blockchain stored in a multi store.
type MultiBlockchainHeader struct {
	PublicKey           *btcec.PublicKey // Public Key of the blockchain
	Height              uint64           // Height is exchanged as uint32 in the protocol, but stored as uint64.
	Version             uint64           // Version is always uint64.
	DateFirstBlockAdded time.Time        // Date the first block was added
	DateLastBlockAdded  time.Time        // Date the last block was added
	ListBlocks          []uint64         // List of block numbers that are stored
	Stats               BlockchainStats  // Statistics about the blockchain (only about stored blocks)
}

// Keep statistics about blocks stored for a blockchain
type BlockchainStats struct {
	CountFileRecords uint64 // Count of file records in stored blocks
	SizeAllFiles     uint64 // Size of all files combined in stored blocks
}

func decodeBlockchainHeader(publicKey *btcec.PublicKey, buffer []byte) (header *MultiBlockchainHeader, err error) {
	if len(buffer) < multiBlockchainHeaderSize {
		return nil, errors.New("header length too small")
	}

	header = &MultiBlockchainHeader{PublicKey: publicKey}
	header.Version = binary.LittleEndian.Uint64(buffer[0:8])
	header.Height = binary.LittleEndian.Uint64(buffer[8:16])
	countBlocks := binary.LittleEndian.Uint64(buffer[16:24])
	header.DateFirstBlockAdded = time.Unix(int64(binary.LittleEndian.Uint64(buffer[24:32])), 0)
	header.DateLastBlockAdded = time.Unix(int64(binary.LittleEndian.Uint64(buffer[32:40])), 0)
	header.Stats.CountFileRecords = binary.LittleEndian.Uint64(buffer[40:48])
	header.Stats.SizeAllFiles = binary.LittleEndian.Uint64(buffer[48:56])

	if uint64(len(buffer)) < multiBlockchainHeaderSize+8*countBlocks {
		return nil, errors.New("header length too small")
	}

	index := multiBlockchainHeaderSize

	for n := uint64(0); n < countBlocks; n++ {
		blockN := binary.LittleEndian.Uint64(buffer[index : index+8])
		header.ListBlocks = append(header.ListBlocks, blockN)
		index += 8
	}

	return header, nil
}

// Reads a blockchains header if available.
func (multi *MultiStore) ReadBlockchainHeader(publicKey *btcec.PublicKey) (header *MultiBlockchainHeader, found bool, err error) {
	buffer, found := multi.Database.Get(publicKey.SerializeCompressed())
	if !found {
		return nil, false, nil
	}

	header, err = decodeBlockchainHeader(publicKey, buffer)
	return header, err == nil, err
}

// WriteBlockchainHeader writes a blockchain header. If one exists, it will be overwritten.
func (multi *MultiStore) WriteBlockchainHeader(header *MultiBlockchainHeader) (err error) {
	raw := make([]byte, multiBlockchainHeaderSize+8*len(header.ListBlocks))

	binary.LittleEndian.PutUint64(raw[0:8], header.Version)
	binary.LittleEndian.PutUint64(raw[8:16], header.Height)
	binary.LittleEndian.PutUint64(raw[16:24], uint64(len(header.ListBlocks)))
	binary.LittleEndian.PutUint64(raw[24:32], uint64(header.DateFirstBlockAdded.UTC().Unix()))
	binary.LittleEndian.PutUint64(raw[32:40], uint64(header.DateLastBlockAdded.UTC().Unix()))
	binary.LittleEndian.PutUint64(raw[40:48], header.Stats.CountFileRecords)
	binary.LittleEndian.PutUint64(raw[48:56], header.Stats.SizeAllFiles)

	index := multiBlockchainHeaderSize

	for _, blockN := range header.ListBlocks {
		binary.LittleEndian.PutUint64(raw[index:index+8], blockN)
		index += 8
	}

	return multi.Database.Set(header.PublicKey.SerializeCompressed(), raw)
}

func lookupKeyForBlock(publicKey *btcec.PublicKey, version, blockNumber uint64) (key []byte) {
	var buffer [16]byte
	binary.LittleEndian.PutUint64(buffer[0:8], version)
	binary.LittleEndian.PutUint64(buffer[8:16], blockNumber)

	key = append(key, publicKey.SerializeCompressed()...)
	key = append(key, buffer[:]...)

	return key
}

// ReadBlock reads a raw block
func (multi *MultiStore) ReadBlock(publicKey *btcec.PublicKey, version, blockNumber uint64) (raw []byte, found bool) {
	return multi.Database.Get(lookupKeyForBlock(publicKey, version, blockNumber))
}

// WriteBlock writes a raw block. It does not update the blockchain header.
func (multi *MultiStore) WriteBlock(publicKey *btcec.PublicKey, version, blockNumber uint64, raw []byte) (err error) {
	return multi.Database.Set(lookupKeyForBlock(publicKey, version, blockNumber), raw)
}

// AssessBlockchainHeader reads the blockchain header, if available, and assesses the status.
func (multi *MultiStore) AssessBlockchainHeader(publicKey *btcec.PublicKey, version, height uint64) (header *MultiBlockchainHeader, status int, err error) {
	// check if there is an existing header for the blockchain
	header, found, err := multi.ReadBlockchainHeader(publicKey)
	if err != nil {
		return nil, MultiStatusErrorReadHeader, err
	} else if !found {
		return nil, MultiStatusHeaderNA, nil
	}

	if header.Version == version && header.Height == height {
		return header, MultiStatusEqual, nil
	}

	// Check if existing version is newer than reported - indicates illegal behavior by the remote peer.
	// Improper refactoring could happen if the local blockchain folder is deleted and automatically recreated.
	if header.Version > version || (header.Version == version && header.Height > height) {
		return header, MultiStatusInvalidRemote, nil
	}

	if version > header.Version {
		return header, MultiStatusNewVersion, nil
	}

	return header, MultiStatusNewBlocks, nil
}

// Deletes an entire blockchain from the store. It will delete each block individually and then the header.
func (multi *MultiStore) DeleteBlockchain(header *MultiBlockchainHeader) {
	// first delete all blocks
	for _, blockN := range header.ListBlocks {
		multi.Database.Delete(lookupKeyForBlock(header.PublicKey, header.Version, blockN))
	}

	// delete the header
	multi.Database.Delete(header.PublicKey.SerializeCompressed())

	if multi.FilterBlockchainDelete != nil {
		multi.FilterBlockchainDelete(multi, header)
	}
}

func (multi *MultiStore) NewBlockchainHeader(publicKey *btcec.PublicKey, version, height uint64) (header *MultiBlockchainHeader, err error) {
	timeN := time.Now().UTC()
	header = &MultiBlockchainHeader{
		PublicKey:           publicKey,
		Height:              height,
		Version:             version,
		DateFirstBlockAdded: timeN,
		DateLastBlockAdded:  timeN,
	}

	return header, multi.WriteBlockchainHeader(header)
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
		if len(key) == 33 {
			// Length 33 indicates key = Public key compressed, value = Header
			if blockchainPublicKey, err := btcec.ParsePubKey(key, btcec.S256()); err == nil {
				if header, err := decodeBlockchainHeader(blockchainPublicKey, value); err == nil {
					callback(header)
				}
			}
		}
	})
}

// IngestBlock ingests a new block into the store. It fails if a block is already stored for the given blockchain and block number.
// It will update the blockchain header including the statistics.
func (multi *MultiStore) IngestBlock(header *MultiBlockchainHeader, blockNumber uint64, raw []byte, failIfInvalid bool) (decoded *BlockDecoded, err error) {
	// check if already exists
	if _, found := multi.ReadBlock(header.PublicKey, header.Version, blockNumber); found {
		return nil, errors.New("already exists")
	}

	// decode it
	decoded, status, err := DecodeBlockRaw(raw)
	if failIfInvalid && err != nil {
		return nil, err
	}

	// store the transferred block in the cache
	multi.WriteBlock(header.PublicKey, header.Version, blockNumber, raw)
	header.ListBlocks = append(header.ListBlocks, blockNumber)

	// update blockchain header stats if records were decoded
	if status == StatusOK {
		multi.UpdateBlockchainStatistics(header, decoded.RecordsDecoded)
	}

	// update the blockchain header
	multi.WriteBlockchainHeader(header)

	return decoded, nil
}
