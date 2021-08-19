/*
File Name:  Block Encoding.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Encoding of records inside blocks.
*/

package core

import (
	"encoding/binary"
	"errors"

	"github.com/PeernetOfficial/core/sanitize"
)

// BlockDecoded contains the decoded records from a block
type BlockDecoded struct {
	Block
	Files       []BlockRecordFile      // Files
	User        BlockRecordUser        // User details
	directories []BlockRecordDirectory // Internal list of directories for decoding files.
}

// RecordTypeX defines the type of the record
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

// decodeBlockRecords decodes the raw records in the block and returns a high-level decoded structure
func decodeBlockRecords(block *Block) (decoded *BlockDecoded, err error) {
	decoded = &BlockDecoded{Block: *block}

	for _, record := range block.RecordsRaw {

		switch record.Type {
		case RecordTypeUsername:
			decoded.User.Name = string(record.Data)
			decoded.User.NameS = sanitize.Username(decoded.User.Name)
			decoded.User.Valid = true

		case RecordTypeDirectory:
			if len(record.Data) < 3 {
				return nil, errors.New("decodeBlockRecord directory record invalid size")
			}

			directory := &BlockRecordDirectory{}
			directory.ID = binary.LittleEndian.Uint16(record.Data[0 : 0+2])
			directory.Name = string(record.Data[2:])
			decoded.directories = append(decoded.directories, *directory)

		case RecordTypeFile:
			if len(record.Data) < 49 {
				return nil, errors.New("decodeBlockRecord file record invalid size")
			}

			file := BlockRecordFile{}
			file.Hash = make([]byte, hashSize)
			copy(file.Hash, record.Data[0:0+hashSize])
			file.Type = record.Data[32]
			file.Format = binary.LittleEndian.Uint16(record.Data[33 : 33+2])
			file.Size = binary.LittleEndian.Uint64(record.Data[35 : 35+8])
			directoryID := binary.LittleEndian.Uint16(record.Data[43 : 43+2])
			filenameSize := binary.LittleEndian.Uint16(record.Data[45 : 45+2])
			//countTags := binary.LittleEndian.Uint16(record.Data[47 : 47+2]) // future implementation of tags

			if len(record.Data) < 49+int(filenameSize) {
				return nil, errors.New("decodeBlockRecord file record invalid filename size")
			}
			file.Name = string(record.Data[49 : 49+filenameSize])

			for n := range decoded.directories {
				if decoded.directories[n].ID == directoryID {
					file.Directory = decoded.directories[n].Name
					break
				}
			}

			decoded.Files = append(decoded.Files, file)
		}
	}

	return decoded, nil
}

// encodeBlockRecordUser encodes the username
func encodeBlockRecordUser(user BlockRecordUser) (recordsRaw []BlockRecordRaw, err error) {
	if user.Valid {
		recordsRaw = append(recordsRaw, BlockRecordRaw{Type: RecordTypeUsername, Data: []byte(user.Name)})
	}

	return recordsRaw, nil
}

// encodeBlockRecordFiles encodes files into the block record data
// This function should be called grouped with all files in the same directory. The directory name is deduplicated; only unique directory records will be returned.
func encodeBlockRecordFiles(files []BlockRecordFile) (recordsRaw []BlockRecordRaw, err error) {
	// First the directory records must be declared for any references by files
	nextDirectoryID := uint16(1) // start as 1 to prevent collision with files without explicit directory
	directoryList := make(map[string]int)

	for n := range files {
		files[n].Directory, files[n].Name = sanitize.Path(files[n].Directory, files[n].Name)

		if files[n].Directory == "" {
			continue
		}

		if directoryID, ok := directoryList[files[n].Directory]; ok {
			files[n].directoryID = uint16(directoryID)
			continue
		}

		// Create the new directory record
		var directoryIDb [2]byte
		binary.LittleEndian.PutUint16(directoryIDb[0:2], nextDirectoryID)
		files[n].directoryID = nextDirectoryID
		nextDirectoryID++

		recordsRaw = append(recordsRaw, BlockRecordRaw{Type: RecordTypeDirectory, Data: append(directoryIDb[:], []byte(files[n].Directory)...)})
	}

	for n := range files {
		var data [49]byte

		if len(files[n].Hash) != hashSize {
			return nil, errors.New("encodeBlockRecords invalid file hash")
		}
		copy(data[0:32], files[n].Hash[0:32])

		data[32] = files[n].Type
		binary.LittleEndian.PutUint16(data[33:33+2], files[n].Format)
		binary.LittleEndian.PutUint64(data[35:35+8], files[n].Size)
		binary.LittleEndian.PutUint16(data[43:43+2], files[n].directoryID)

		filenameB := []byte(files[n].Name)
		binary.LittleEndian.PutUint16(data[45:45+2], uint16(len(filenameB)))
		binary.LittleEndian.PutUint16(data[47:47+2], uint16(0)) // Count of Tags (future use)

		recordsRaw = append(recordsRaw, BlockRecordRaw{Type: RecordTypeFile, Data: append(data[:], filenameB...)})
	}

	return recordsRaw, nil
}
