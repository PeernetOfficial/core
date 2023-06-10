/*
File Username:  Block Record File.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

File records:
Offset  Size    Info
0       32      Hash blake3 of the file content
32      16      File ID
48      32      Merkle Root Hash
80      8       Fragment Size
88      1       File Type
89      2       File Format
91      8       File Size
99      2       Count of Tags
101     ?       Tags

Each file tag provides additional optional information:
Offset  Size    Info
0       2       Type
2       4       Size of data that follows
6       ?       Data according to the tag type

Tag data record contains only raw data and may be referenced by Tags in File records.
This is a basic embedded way of compression when tags are repetitive in multiple files within the same block.

*/

package blockchain

import (
	"encoding/binary"
	"errors"
	"math"

	"github.com/PeernetOfficial/core/protocol"
	"github.com/google/uuid"
)

// BlockRecordFile is the metadata of a file published on the blockchain
type BlockRecordFile struct {
	Hash           []byte               // Hash of the file data
	ID             uuid.UUID            // ID of the file
	MerkleRootHash []byte               // Merkle Root Hash
	FragmentSize   uint64               // Fragment Size
	Type           uint8                // File Type
	Format         uint16               // File Format
	Size           uint64               // Size of the file data
	NodeID         []byte               // Node ID, owner of the file
	Tags           []BlockRecordFileTag // Tags provide additional metadata
	Username       string               // Username of the User who uploaded the file
}

// BlockRecordFileTag provides metadata about the file.
type BlockRecordFileTag struct {
	Type uint16 // See TagX constants.
	Data []byte // Data

	// If top bit of Type is set, then Data must be 2, 4, or 8 bytes representing the distance number (positive or negative) of raw record in the block that will be used as data.
	// This is an embedded basic compression algorithm for repetitive tag. For example directory tags or album tags might be heavily repetitive among files.
}

const blockRecordFileMinSize = 101

// decodeBlockRecordFiles decodes only file records. Other records are ignored.
func decodeBlockRecordFiles(recordsRaw []BlockRecordRaw, nodeID []byte) (files []BlockRecordFile, err error) {
	for i, record := range recordsRaw {
		switch record.Type {
		case RecordTypeFile:
			if len(record.Data) < blockRecordFileMinSize {
				return nil, errors.New("file record invalid size")
			}

			file := BlockRecordFile{NodeID: nodeID}
			file.Hash = make([]byte, protocol.HashSize)
			copy(file.Hash, record.Data[0:0+protocol.HashSize])
			copy(file.ID[:], record.Data[32:32+16])

			file.MerkleRootHash = make([]byte, protocol.HashSize)
			copy(file.MerkleRootHash, record.Data[48:48+protocol.HashSize])
			file.FragmentSize = binary.LittleEndian.Uint64(record.Data[80 : 80+8])

			file.Type = record.Data[88]
			file.Format = binary.LittleEndian.Uint16(record.Data[89 : 89+2])
			file.Size = binary.LittleEndian.Uint64(record.Data[91 : 91+8])

			countTags := binary.LittleEndian.Uint16(record.Data[99 : 99+2])

			index := blockRecordFileMinSize

			for n := uint16(0); n < countTags; n++ {
				if index+6 > len(record.Data) {
					return nil, errors.New("file record tags invalid size")
				}

				tag := BlockRecordFileTag{}
				tag.Type = binary.LittleEndian.Uint16(record.Data[index:index+2]) & 0x7FFF
				tagSize := binary.LittleEndian.Uint32(record.Data[index+2 : index+2+4])
				isDataReference := record.Data[index+1]&0x80 != 0

				if index+6+int(tagSize) > len(record.Data) {
					return nil, errors.New("file record tag data invalid size")
				}

				if isDataReference { // reference to RecordTypeTagData record?
					var refRecordNumber int
					if tagSize == 2 {
						refRecordNumber = i + int(int16(binary.LittleEndian.Uint16(record.Data[index+6:index+6+2])))
					} else if tagSize == 4 {
						refRecordNumber = i + int(int32(binary.LittleEndian.Uint32(record.Data[index+6:index+6+4])))
					} else if tagSize == 8 {
						refRecordNumber = i + int(int64(binary.LittleEndian.Uint64(record.Data[index+6:index+6+8])))
					} else {
						return nil, errors.New("file record tag reference invalid size")
					}

					if refRecordNumber < 0 || refRecordNumber >= len(recordsRaw) {
						return nil, errors.New("file record tag reference not available")
					} else if recordsRaw[refRecordNumber].Type != RecordTypeTagData {
						return nil, errors.New("file record tag reference invalid")
					}

					tag.Data = recordsRaw[refRecordNumber].Data

				} else {
					tag.Data = record.Data[index+6 : index+6+int(tagSize)]
				}

				file.Tags = append(file.Tags, tag)

				index += 6 + int(tagSize)
			}

			file.Tags = append(file.Tags, TagFromDate(TagDateShared, record.Date))

			files = append(files, file)
		}
	}

	return files, err
}

// encodeBlockRecordFiles encodes files into the block record data
// This function should be called grouped with all files in the same folder. The folder name is deduplicated; only unique folder records will be returned.
// Note that this function only stores the folder names as tags; it does not create separate TypeFolder file records.
func encodeBlockRecordFiles(files []BlockRecordFile) (recordsRaw []BlockRecordRaw, err error) {
	uniqueTagDataMap := make(map[string]struct{})
	duplicateTagDataMap := make(map[string]int) // list of tag data that appeared twice. Number in recordsRaw.

	// loop through all tags to encode them and create list of duplicates that will be replaced by references
	for n := range files {
		for _, tag := range files[n].Tags {
			if len(tag.Data) > 4 {
				if _, ok := uniqueTagDataMap[string(tag.Data)]; !ok {
					uniqueTagDataMap[string(tag.Data)] = struct{}{}
				} else if _, ok := duplicateTagDataMap[string(tag.Data)]; !ok {
					recordsRaw = append(recordsRaw, BlockRecordRaw{Type: RecordTypeTagData, Data: tag.Data})
					duplicateTagDataMap[string(tag.Data)] = len(recordsRaw) - 1
				}
			}
		}
	}

	// then encode all files as records
	for n := range files {
		data := make([]byte, blockRecordFileMinSize)

		if len(files[n].Hash) != protocol.HashSize {
			return nil, errors.New("encodeBlockRecords invalid file hash")
		} else if len(files[n].MerkleRootHash) != protocol.HashSize {
			return nil, errors.New("encodeBlockRecords invalid merkle root hash")
		}

		copy(data[0:32], files[n].Hash[0:32])
		copy(data[32:32+16], files[n].ID[:])
		copy(data[48:48+32], files[n].MerkleRootHash[0:32])
		binary.LittleEndian.PutUint64(data[80:80+8], files[n].FragmentSize)

		data[88] = files[n].Type
		binary.LittleEndian.PutUint16(data[89:89+2], files[n].Format)
		binary.LittleEndian.PutUint64(data[91:91+8], files[n].Size)

		var tagCount uint16

		for _, tag := range files[n].Tags {
			// Some tags are virtual and never stored on the blockchain. If attempted to write, ignore.
			if tag.IsVirtual() {
				continue
			}
			tagCount++

			if len(tag.Data) > 4 {
				if refNumber, ok := duplicateTagDataMap[string(tag.Data)]; ok {
					// In case the data is duplicated, use reference to the RecordTypeTagData instead
					tag.Type |= 0x8000
					tag.Data = intToBytes(-(len(recordsRaw) - refNumber))
				}
			}

			var tempTag [6]byte

			binary.LittleEndian.PutUint16(tempTag[0:2], tag.Type)
			binary.LittleEndian.PutUint32(tempTag[2:2+4], uint32(len(tag.Data)))

			data = append(data, tempTag[:]...)
			data = append(data, tag.Data...)
		}

		binary.LittleEndian.PutUint16(data[99:99+2], tagCount)

		recordsRaw = append(recordsRaw, BlockRecordRaw{Type: RecordTypeFile, Data: data})
	}

	return recordsRaw, nil
}

// intToBytes encodes int to little endian byte array as it fits to 16, 32 or 64 bit.
func intToBytes(number int) (buffer []byte) {
	buffer = make([]byte, 4)

	if number <= math.MaxInt16 && number >= math.MinInt16 {
		binary.LittleEndian.PutUint16(buffer[0:2], uint16(number))
		return buffer[0:2]
	} else if number <= math.MaxInt32 && number >= math.MinInt32 {
		binary.LittleEndian.PutUint32(buffer[0:4], uint32(number))
		return buffer[0:4]
	}

	binary.LittleEndian.PutUint64(buffer[0:8], uint64(number))
	return buffer[0:8]
}

// SizeInBlock returns the full size this file takes up in a single block. (i.e., the record size)
// If paired with other files in a single block, compression (via tag references) may reduce the actual size.
func (file *BlockRecordFile) SizeInBlock() (size uint64) {
	size = blockRecordHeaderSize + blockRecordFileMinSize

	for _, tag := range file.Tags {
		if tag.IsVirtual() {
			continue
		}

		size += 6 + uint64(len(tag.Data))
	}

	return size
}
