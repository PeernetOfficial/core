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

// SearchIndexRecord identifies a hash to a given file
type SearchIndexRecord struct {
	// input data
	Word string
	Hash []byte

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
	if index.Database == nil {
		return
	}

	// decode all files from the block
	decoded, status, err := blockchain.DecodeBlockRaw(raw)
	if err != nil || status != blockchain.StatusOK {
		return
	}

	for _, decodedR := range decoded.RecordsDecoded {
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

func (index *SearchIndexStore) UnindexBlock(publicKey *btcec.PublicKey, blockchainVersion, blockNumber uint64) {
	if index.Database == nil {
		return
	}

	// get the reverse records
	key := reverseIndexKey(publicKey, blockchainVersion, blockNumber)
	raw, found := index.Database.Get(key)

	if !found || len(raw)%reverseIndexRecordSize != 0 { // corrupt record
		return
	}

	offset := 0

	for n := 0; n < len(raw)/reverseIndexRecordSize; n++ {
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
	if index.Database == nil {
		return
	}

	index.Lock()
	defer index.Unlock()

	// parse existing records, check if already stored
	raw, found := index.Database.Get(hash)
	if found && len(raw)%indexRecordSize == 0 { // check if record is corrupt
		offset := 0
		for n := 0; n < len(raw)/indexRecordSize; n++ {
			if record := decodeIndexRecord(raw[offset : offset+indexRecordSize]); record != nil {
				if fileID == record.FileID {
					return errors.New("already indexed")
				}
			}

			offset += indexRecordSize
		}
	}

	raw = append(raw, encodeIndexRecord(publicKey, blockchainVersion, blockNumber, fileID)...)

	// create the reverse record
	index.createReverseIndexRecord(publicKey, blockchainVersion, blockNumber, fileID, hash)

	return index.Database.Set(hash, raw)
}

// UnindexHash deletes a index record. If there are no more files associated with the hash, the entire hash record is deleted.
func (index *SearchIndexStore) UnindexHash(fileID uuid.UUID, hash []byte) (err error) {
	if index.Database == nil {
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
		offset := 0
		for n := 0; n < len(raw)/indexRecordSize; n++ {
			if record := decodeIndexRecord(raw[offset : offset+indexRecordSize]); record != nil {
				if fileID != record.FileID {
					newRaw = append(newRaw, raw[offset:offset+indexRecordSize]...)
				}
			}

			offset += indexRecordSize
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
func (index *SearchIndexStore) LookupHash(hash []byte) (records []SearchIndexRecord, err error) {
	if index.Database == nil {
		return
	}

	index.RLock()
	defer index.RUnlock()

	raw, found := index.Database.Get(hash)
	if !found {
		return nil, nil
	} else if len(raw)%indexRecordSize != 0 { // check if record is corrupt
		return nil, errors.New("corrupt index record")
	}

	for offset := 0; offset < len(raw); offset += indexRecordSize {
		if record := decodeIndexRecord(raw[offset : offset+indexRecordSize]); record != nil {
			record.Hash = hash
			records = append(records, *record)
		}
	}

	return records, nil
}

// ---- index and reverse index code ----

/*
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

// This creates a reverse index record. It uses the blockchain and block number as key, and provides the hash and file ID as value.
// This function must be called in a RW locked database state. The caller must ensure that this does not result in a duplicate.
func (index *SearchIndexStore) createReverseIndexRecord(publicKey *btcec.PublicKey, blockchainVersion, blockNumber uint64, fileID uuid.UUID, hash []byte) (err error) {
	key := reverseIndexKey(publicKey, blockchainVersion, blockNumber)
	raw, _ := index.Database.Get(key)

	// each record is only hash + file ID
	reverseRecord := make([]byte, reverseIndexRecordSize)
	copy(reverseRecord[0:32], hash)
	copy(reverseRecord[32:32+16], fileID[:])

	raw = append(raw, reverseRecord...)

	return index.Database.Set(key, raw)
}

const reverseIndexRecordSize = 32 + 16

func reverseIndexKey(publicKey *btcec.PublicKey, blockchainVersion, blockNumber uint64) (key []byte) {
	key = publicKey.SerializeCompressed()

	var temp [16]byte
	binary.LittleEndian.PutUint64(temp[0:8], blockchainVersion)
	binary.LittleEndian.PutUint64(temp[8:16], blockNumber)
	key = append(key, temp[:]...)

	return key
}
