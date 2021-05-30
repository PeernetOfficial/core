/*
File Name:  Block Encoding.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Block encoding in messages.
*/

package core

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/btcsuite/btcd/btcec"
)

// Block is a single block containing a set of records (metadata).
// It has no upper size limit, although a soft limit of 64 KB - overhead is encouraged for efficiency.
type Block struct {
	OwnerPublicKey    *btcec.PublicKey       // Owner Public Key, ECDSA (secp256k1) 257-bit
	LastBlockHash     []byte                 // Hash of the last block. Blake3.
	BlockchainVersion uint64                 // Blockchain version
	Number            uint32                 // Block number
	RecordsRaw        []BlockRecordRaw       // Block records raw
	Files             []BlockRecordFile      // Files
	User              BlockRecordUser        // User details
	directories       []BlockRecordDirectory // Internal list of directories for decoding files.
}

// BlockRecordRaw is a single block record (not decoded)
type BlockRecordRaw struct {
	Type uint8  // Record Type. See RecordTypeX.
	Data []byte // Data according to the type
}

const (
	RecordTypeUsername      = 0 // Username. Arbitrary name defined by the user.
	RecordTypeDirectory     = 1 // Directory. Only valid in the context of the current block.
	RecordTypeFile          = 2 // File
	RecordTypeContentRating = 3 // Content rating (positive).
	RecordTypeContentReport = 4 // Content report (negative).
	RecordTypeDelete        = 5 // Delete previous record.
)

// block record structures

// BlockRecordUser specifies user information
type BlockRecordUser struct {
	Valid bool   // Whether the username is supplied
	Name  string // Arbitrary name of the user.
	NameS string // Sanitized version of the name.
}

// BlockRecordDirectory is a directory, only valid within the same block.
type BlockRecordDirectory struct {
	ID   uint16 // ID, only valid within the same block
	Name string // Name of the directory. Slashes (both backward and forward) mark subdirectories.
}

// BlockRecordFile is the metadata of a file published on the blockchain
type BlockRecordFile struct {
	Hash        []byte // Hash of the file data
	Type        uint8  // Type (low-level)
	Format      uint16 // Format (high-level)
	Size        uint64 // Size of the file
	Directory   string // Directory
	Name        string // File name
	directoryID uint16 // Internal directory ID
	// Tags todo
}

// Tag structure to be defined

const blockHeaderSize = 115
const blockRecordHeaderSize = 5

// decodeBlock decodes a single block
func decodeBlock(raw []byte) (block *Block, err error) {
	if len(raw) < blockHeaderSize {
		return nil, errors.New("decodeBlock invalid block size")
	}

	block = &Block{}

	signature := raw[0 : 0+65]

	block.OwnerPublicKey, _, err = btcec.RecoverCompact(btcec.S256(), signature, hashData(raw[65:]))
	if err != nil {
		return nil, err
	}

	block.LastBlockHash = make([]byte, hashSize)
	copy(block.LastBlockHash, raw[65:65+hashSize])

	block.BlockchainVersion = binary.LittleEndian.Uint64(raw[97 : 97+8])
	block.Number = binary.LittleEndian.Uint32(raw[105 : 105+4])

	blockSize := binary.LittleEndian.Uint32(raw[109 : 109+4])
	if blockSize != uint32(len(raw)) {
		return nil, errors.New("decodeBlock invalid block size")
	}

	countRecords := binary.LittleEndian.Uint16(raw[113 : 113+2])
	index := 115

	for n := uint16(0); n < countRecords; n++ {
		if index+blockRecordHeaderSize > len(raw) {
			return nil, errors.New("decodeBlock block record exceeds block size")
		}

		recordType := raw[index]
		recordSize := binary.LittleEndian.Uint32(raw[index+1 : index+5])
		index += blockRecordHeaderSize

		if index+int(recordSize) > len(raw) {
			return nil, errors.New("decodeBlock block record exceeds block size")
		}

		block.RecordsRaw = append(block.RecordsRaw, BlockRecordRaw{Type: recordType, Data: raw[index : index+int(recordSize)]})

		if err := decodeBlockRecord(raw[index:index+int(recordSize)], block, recordType); err != nil {
			return nil, err
		}

		index += int(recordSize)
	}

	return block, nil
}

// decodeBlockRecord decodes a single block and fills it into the provided block structure
func decodeBlockRecord(data []byte, block *Block, recordType uint8) (err error) {
	switch recordType {
	case RecordTypeUsername:
		block.User.Name = string(data)
		block.User.NameS = sanitizeUsername(block.User.Name)
		block.User.Valid = true

	case RecordTypeDirectory:
		if len(data) < 3 {
			return errors.New("decodeBlockRecord directory record invalid size")
		}

		directory := &BlockRecordDirectory{}
		directory.ID = binary.LittleEndian.Uint16(data[0 : 0+2])
		directory.Name = string(data[2:])
		block.directories = append(block.directories, *directory)

	case RecordTypeFile:
		if len(data) < 49 {
			return errors.New("decodeBlockRecord file record invalid size")
		}

		file := BlockRecordFile{}
		file.Hash = make([]byte, hashSize)
		copy(file.Hash, data[0:0+hashSize])
		file.Type = data[32]
		file.Format = binary.LittleEndian.Uint16(data[33 : 33+2])
		file.Size = binary.LittleEndian.Uint64(data[35 : 35+8])
		directoryID := binary.LittleEndian.Uint16(data[43 : 43+2])
		filenameSize := binary.LittleEndian.Uint16(data[45 : 45+2])
		//countTags := binary.LittleEndian.Uint16(data[47 : 47+2]) // future implementation of tags

		if len(data) < 49+int(filenameSize) {
			return errors.New("decodeBlockRecord file record invalid filename size")
		}
		file.Name = string(data[49 : 49+filenameSize])

		for n := range block.directories {
			if block.directories[n].ID == directoryID {
				file.Directory = block.directories[n].Name
				break
			}
		}

		block.Files = append(block.Files, file)
	}

	return nil
}

// sanitizeUsername returns the sanitized version of the username.
func sanitizeUsername(input string) string {
	if !utf8.ValidString(input) {
		return "<invalid encoding>"
	}

	input = strings.TrimSpace(input)
	input = strings.ReplaceAll(input, "\n", " ")
	input = strings.ReplaceAll(input, "\r", "")

	// Max length for sanitized version is 36, resembling the limit from StackOverflow.
	if len(input) > 36 {
		input = input[:36]
	}

	return input
}

func encodeBlock(block *Block, ownerPrivateKey *btcec.PrivateKey) (raw []byte, err error) {
	var buffer bytes.Buffer
	buffer.Write(make([]byte, 65)) // Signature, filled at the end

	if block.Number > 0 && len(block.LastBlockHash) != hashSize {
		return nil, errors.New("encodeBlock invalid last block hash")
	} else if block.Number == 0 { // Block 0: Empty last hash
		block.LastBlockHash = make([]byte, 32)
	}
	buffer.Write(block.LastBlockHash)

	var temp [8]byte
	binary.LittleEndian.PutUint64(temp[0:8], block.BlockchainVersion)
	buffer.Write(temp[:])

	binary.LittleEndian.PutUint32(temp[0:4], block.Number)
	buffer.Write(temp[:4])

	buffer.Write(make([]byte, 4)) // Size of block, filled later
	buffer.Write(make([]byte, 2)) // Count of records, filled later

	// write all records
	countRecords, err := encodeBlockRecords(&buffer, block)
	if err != nil {
		return nil, err
	}

	// finalize the block
	raw = buffer.Bytes()
	if len(raw) < blockHeaderSize {
		return nil, errors.New("encodeBlock invalid block size")
	}

	binary.LittleEndian.PutUint32(raw[109:109+4], uint32(len(raw))) // Size of block
	binary.LittleEndian.PutUint16(raw[113:113+2], countRecords)     // Count of records

	// signature is last
	signature, err := btcec.SignCompact(btcec.S256(), ownerPrivateKey, hashData(raw[65:]), true)
	if err != nil {
		return nil, err
	}
	copy(raw[0:0+65], signature)

	return raw, nil
}

// encodeBlockRecords writes all records of the block into the writer
func encodeBlockRecords(writer io.Writer, block *Block) (count uint16, err error) {
	nextDirectoryID := uint16(1) // start as 1 to prevent collision with files without explicit directory

	writeBlockRecord := func(recordType uint8, data []byte) {
		var temp [8]byte
		binary.LittleEndian.PutUint32(temp[0:4], uint32(len(data)))

		writer.Write([]byte{recordType}) // Record Type
		writer.Write(temp[:4])           // Size of data
		writer.Write(data)               // Data

		count++
	}

	// Username
	if block.User.Valid {
		writeBlockRecord(RecordTypeUsername, []byte(block.User.Name))
	}

	// First the directory records must be declared for any references by files
directoryCreateLoop:
	for n := range block.Files {
		block.Files[n].sanitizePath()
		if block.Files[n].Directory == "" {
			continue
		}

		// already known directory ID?
		for m := range block.directories {
			if block.directories[m].Name == block.Files[n].Directory {
				block.Files[n].directoryID = block.directories[m].ID
				continue directoryCreateLoop
			}
		}

		// Create the new directory record
		var directoryID [2]byte
		binary.LittleEndian.PutUint16(directoryID[0:2], nextDirectoryID)
		block.Files[n].directoryID = nextDirectoryID
		nextDirectoryID++

		writeBlockRecord(RecordTypeDirectory, append(directoryID[:], []byte(block.Files[n].Directory)...))
	}

	for n := range block.Files {
		var data [49]byte

		if len(block.Files[n].Hash) != hashSize {
			return 0, errors.New("encodeBlockRecords invalid file hash")
		}
		copy(data[0:32], block.Files[n].Hash[0:32])

		data[32] = block.Files[n].Type
		binary.LittleEndian.PutUint16(data[33:33+2], block.Files[n].Format)
		binary.LittleEndian.PutUint64(data[35:35+8], block.Files[n].Size)
		binary.LittleEndian.PutUint16(data[43:43+2], block.Files[n].directoryID)

		filenameB := []byte(block.Files[n].Name)
		binary.LittleEndian.PutUint16(data[45:45+2], uint16(len(filenameB)))
		binary.LittleEndian.PutUint16(data[47:47+2], uint16(0)) // Count of Tags (future use)

		writeBlockRecord(RecordTypeFile, append(data[:], filenameB...))
	}

	return count, nil
}

const PATH_MAX_LENGTH = 32767 // Windows Maximum Path Length for UNC paths

func (record *BlockRecordFile) sanitizePath() {
	// Enforced forward slashes as directory separator and clean the path.
	record.Directory = strings.ReplaceAll(record.Directory, "\\", "/")
	record.Directory = path.Clean(record.Directory)

	// No slash at the beginning and end to save space.
	record.Directory = strings.Trim(record.Directory, "/")

	// Slashes in filenames are not encouraged, but not removed.

	// Enforce max filename length.
	if len(record.Name) > PATH_MAX_LENGTH {
		record.Name = record.Name[:PATH_MAX_LENGTH]
	}
}
