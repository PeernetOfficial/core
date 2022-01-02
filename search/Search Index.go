/*
File Name:  Search Index.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package search

import (
	"encoding/binary"
	"errors"
	"sync"

	"github.com/PeernetOfficial/core/blockchain"
	"github.com/PeernetOfficial/core/btcec"
	"github.com/PeernetOfficial/core/store"
	"github.com/google/uuid"
)

// A search selector is a term that discovers a file.
type SearchSelector struct {
	Word        string // Normalized version of the word
	Hash        []byte // Hash of the word
	ExactSearch bool   // Indicates this is an exact search term, for example a full filename.
}

// SearchIndexRecord identifies a hash to a given file
type SearchIndexRecord struct {
	// List of selectors that found the result. Multiple keywords may find the same file.
	Selectors []SearchSelector

	// result data
	FileID            uuid.UUID
	PublicKey         *btcec.PublicKey
	BlockchainVersion uint64
	BlockNumber       uint64
}

// This database stores hashes of keywords for file search.
type SearchIndexStore struct {
	Database store.Store // The database storing the blockchain.
	sync.RWMutex
}

func InitSearchIndexStore(DatabaseDirectory string) (searchIndex *SearchIndexStore, err error) {
	if DatabaseDirectory == "" {
		return
	}

	searchIndex = &SearchIndexStore{}

	if searchIndex.Database, err = store.NewPogrebStore(DatabaseDirectory); err != nil {
		return nil, err
	}

	return searchIndex, nil
}

func (index *SearchIndexStore) IndexNewBlock(publicKey *btcec.PublicKey, blockchainVersion, blockNumber uint64, raw []byte) {
	if index == nil {
		return
	}

	// decode all files from the block
	decoded, status, err := blockchain.DecodeBlockRaw(raw)
	if err != nil || status != blockchain.StatusOK {
		return
	}

	index.IndexNewBlockDecoded(publicKey, blockchainVersion, blockNumber, decoded.RecordsDecoded)
}

// Indexes a new decoded block. Currently it only indexes file records.
func (index *SearchIndexStore) IndexNewBlockDecoded(publicKey *btcec.PublicKey, blockchainVersion, blockNumber uint64, recordsDecoded []interface{}) {
	if index == nil {
		return
	}

	for _, decodedR := range recordsDecoded {
		if file, ok := decodedR.(blockchain.BlockRecordFile); ok {
			var filename, folder, description string
			for _, tag := range file.Tags {
				switch tag.Type {
				case blockchain.TagName:
					filename = sanitizeGeneric(tag.Text())
				case blockchain.TagFolder:
					folder = sanitizeGeneric(tag.Text())
				case blockchain.TagDescription:
					description = sanitizeGeneric(tag.Text())
				}
			}

			hashes := make(map[[32]byte]string)
			filename2Hashes(filename, folder, hashes)
			text2Hashes(description, hashes)

			for hash := range hashes {
				index.IndexHash(publicKey, blockchainVersion, blockNumber, file.ID, hash[:])
			}
		}
	}
}

// UnindexBlockchain deletes all index for a given blockchain. This is intentionally not done on a version/block level, because it could easily lead to orphans.
func (index *SearchIndexStore) UnindexBlockchain(publicKey *btcec.PublicKey) {
	if index == nil {
		return
	}

	// get the reverse record
	key := publicKey.SerializeCompressed()
	raw, found := index.Database.Get(key)

	if !found || len(raw)%reverseIndexRecordSize != 0 { // corrupt record
		return
	}

	for offset := 0; offset < len(raw); offset += reverseIndexRecordSize {
		var hash []byte
		var fileID uuid.UUID

		hash = raw[offset : offset+32]
		copy(fileID[:], raw[offset+32:offset+32+16])

		// delete the index record
		index.UnindexHash(fileID, hash)
	}

	// delete the reverse record
	index.Database.Delete(key)
}

// IndexHash indexes a new hash
func (index *SearchIndexStore) IndexHash(publicKey *btcec.PublicKey, blockchainVersion, blockNumber uint64, fileID uuid.UUID, hash []byte) (err error) {
	if index == nil {
		return
	}

	index.Lock()
	defer index.Unlock()

	// parse existing records, check if already stored
	raw, found := index.Database.Get(hash)
	if found && len(raw)%indexRecordSize == 0 { // check if record is corrupt
		for offset := 0; offset < len(raw); offset += indexRecordSize {
			if record := decodeIndexRecord(raw[offset : offset+indexRecordSize]); record != nil {
				if fileID == record.FileID {
					return errors.New("already indexed")
				}
			}
		}
	}

	raw = append(raw, encodeIndexRecord(publicKey, blockchainVersion, blockNumber, fileID)...)

	// create the reverse record
	index.createReverseIndexRecord(publicKey, blockchainVersion, blockNumber, fileID, hash)

	return index.Database.Set(hash, raw)
}

// UnindexHash deletes a index record. If there are no more files associated with the hash, the entire hash record is deleted.
func (index *SearchIndexStore) UnindexHash(fileID uuid.UUID, hash []byte) (err error) {
	if index == nil {
		return
	}

	index.Lock()
	defer index.Unlock()

	var newRaw []byte

	raw, found := index.Database.Get(hash)
	if !found {
		return errors.New("index record not found")
	}

	if len(raw)%indexRecordSize == 0 { // check if record is corrupt
		for offset := 0; offset < len(raw); offset += indexRecordSize {
			if record := decodeIndexRecord(raw[offset : offset+indexRecordSize]); record != nil {
				if fileID != record.FileID {
					newRaw = append(newRaw, raw[offset:offset+indexRecordSize]...)
				}
			}
		}
	}

	if len(newRaw) == 0 {
		// delete the entire hash key
		index.Database.Delete(hash)
		return
	}

	return index.Database.Set(hash, newRaw)
}

// LookupHash returns all index records stored for the hash.
func (index *SearchIndexStore) LookupHash(selector SearchSelector, resultMap map[uuid.UUID]*SearchIndexRecord) (err error) {
	if index == nil {
		return
	}

	index.RLock()
	defer index.RUnlock()

	raw, found := index.Database.Get(selector.Hash)
	if !found {
		return nil
	} else if len(raw)%indexRecordSize != 0 { // check if record is corrupt
		return errors.New("corrupt index record")
	}

	for offset := 0; offset < len(raw); offset += indexRecordSize {
		if record := decodeIndexRecord(raw[offset : offset+indexRecordSize]); record != nil {
			if existingRecord, ok := resultMap[record.FileID]; ok {
				existingRecord.Selectors = append(existingRecord.Selectors, selector)
			} else {
				record.Selectors = []SearchSelector{selector}
				resultMap[record.FileID] = record
			}
		}
	}

	return nil
}

// ---- index and reverse index code ----

/*
Index records are records looked up based on a hash (the search term) to uniquely identify a single file.
Structure for each index record:

Offset   Size    Info
0        16      File ID
16       33      Public Key compressed
49       8       Blockchain Version
57       8       Block Number
*/

const indexRecordSize = 65

// decodeIndexRecord decodes the index record and sets the fields File ID, Public Key, and Block Number.
func decodeIndexRecord(raw []byte) (record *SearchIndexRecord) {
	if len(raw) < indexRecordSize {
		return
	}

	record = &SearchIndexRecord{}
	copy(record.FileID[:], raw[0:16])

	var err error
	if record.PublicKey, err = btcec.ParsePubKey(raw[16:16+33], btcec.S256()); err != nil {
		return nil
	}

	record.BlockchainVersion = binary.LittleEndian.Uint64(raw[49 : 49+8])
	record.BlockNumber = binary.LittleEndian.Uint64(raw[57 : 57+8])

	return record
}

func encodeIndexRecord(publicKey *btcec.PublicKey, blockchainVersion, blockNumber uint64, fileID uuid.UUID) (raw []byte) {
	raw = make([]byte, indexRecordSize)

	copy(raw[0:16], fileID[:])
	copy(raw[16:16+33], publicKey.SerializeCompressed())
	binary.LittleEndian.PutUint64(raw[49:49+8], blockchainVersion)
	binary.LittleEndian.PutUint64(raw[57:57+8], blockNumber)

	return raw
}

// Reverse index records keep track of all hashes and file IDs searchable for a given blockchain.
const reverseIndexRecordSize = 32 + 16

// This creates a reverse index record. It uses the blockchain and block number as key, and provides the hash and file ID as value.
// This function must be called in a RW locked database state. The caller must ensure that this does not result in a duplicate.
func (index *SearchIndexStore) createReverseIndexRecord(publicKey *btcec.PublicKey, blockchainVersion, blockNumber uint64, fileID uuid.UUID, hash []byte) (err error) {
	key := publicKey.SerializeCompressed()
	raw, _ := index.Database.Get(key)

	// each record is only hash + file ID
	reverseRecord := make([]byte, reverseIndexRecordSize)
	copy(reverseRecord[0:32], hash)
	copy(reverseRecord[32:32+16], fileID[:])

	raw = append(raw, reverseRecord...)

	return index.Database.Set(key, raw)
}
