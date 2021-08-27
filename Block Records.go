/*
File Name:  Block Encoding.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

This files defines the encoding of blocks and records within.

File records:
Offset  Size    Info
0       32      Hash blake3 of the file content
32      16      File ID
48      1       File Type (low-level)
49      2       File Format (high-level)
51      8       File Size
59      2       Count of Tags
61      ?       Tags

Each file tag provides additional optional information:
Offset  Size    Info
0       2       Type
2       4       Size of data that follows
6       ?       Data according to the tag type

Tag data record contains only raw data and may be referenced by Tags in File records.
This is a basic embedded way of compression when tags are repetitive in multiple files within the same block.

*/

package core

import (
	"encoding/binary"
	"errors"
	"math"
	"time"

	"github.com/PeernetOfficial/core/sanitize"
	"github.com/google/uuid"
)

// ---- Block record structures (decoded) ----

// RecordTypeX defines the type of the record
const (
	RecordTypeUsername      = 0 // Username. Arbitrary name defined by the user.
	RecordTypeTagData       = 1 // Tag data record to be referenced by one or multiple tags. Only valid in the context of the current block.
	RecordTypeFile          = 2 // File
	RecordTypeDelete        = 3 // Delete previous record by ID.
	RecordTypeCertificate   = 4 // Certificate to certify provided information in the blockchain issued by a trusted 3rd party.
	RecordTypeContentRating = 5 // Content rating (positive).
	RecordTypeContentReport = 6 // Content report (negative).
)

// BlockRecordUser specifies user information
type BlockRecordUser struct {
	Name          string // Arbitrary name of the user (original input).
	NameSanitized string // Sanitized version of the name.
}

// BlockRecordFile is the metadata of a file published on the blockchain
type BlockRecordFile struct {
	Hash        []byte               // Hash of the file data
	ID          uuid.UUID            // ID
	Type        uint8                // Type (low-level)
	Format      uint16               // Format (high-level)
	Size        uint64               // Size of the file data
	TagsRaw     []BlockRecordFileTag // Tags to provide additional metadata
	TagsDecoded []interface{}        // Decoded tags. See FileTagX structures.
}

// ---- Tag structures ----

// BlockRecordFileTag provides additional metadata about the file.
// New tags can be defines in the future without breaking support.
type BlockRecordFileTag struct {
	Type uint16 // Type (low-level). See TagTypeX constants.
	Data []byte // Actual data of the tag, decoded into FileTagX structures.

	// If top bit of Type is set, then Data must be 2, 4, or 8 bytes representing the distance number (positive or negative) of raw record in the block that will be used as data.
	// This is an embedded basic compression algorithm for repetitive tag. For example directory tags or album tags might be heavily repetitive among files.
}

// TagTypeName defines the type of the tag
const (
	TagTypeName        = 0 // Name of file
	TagTypeFolder      = 1 // Folder
	TagTypeDateCreated = 2 // Date when the file was originally created. This may differ from the date in the block record, which indicates when the file was shared.
	TagTypeDescription = 3 // Arbitrary description of the file. May contain hashtags.
)

// FileTagFolder specifies in which folder the file is stored.
// A corresponding TypeFolder file record matching the name may exist to provide additional details about the folder.
type FileTagFolder struct {
	Name string // Name of the folder
}

// FileTagName specifies the file name. Empty names are allowed, but not recommended!
type FileTagName struct {
	Name string // Name of the file
}

// FileTagDescription is an arbitrary of the description of the file. It may contain hashtags to help tagging and searching.
type FileTagDescription struct {
	Description string
}

// FileTagCreated is the date when the file was originally created
type FileTagCreated struct {
	Date time.Time // Created time
}

// ---- low-level encoding ----

// decodeBlockRecordUser decodes the username, if available. Otherwise return nil.
func decodeBlockRecordUser(recordsRaw []BlockRecordRaw) (user *BlockRecordUser) {
	for _, record := range recordsRaw {
		if record.Type == RecordTypeUsername {
			user = &BlockRecordUser{Name: string(record.Data), NameSanitized: sanitize.Username(string(record.Data))}
			// continue to seek for an overriding username record, even though that would be stupid within the same block
		}
	}
	return
}

// encodeBlockRecordUser encodes the username
func encodeBlockRecordUser(user BlockRecordUser) (recordsRaw []BlockRecordRaw, err error) {
	recordsRaw = append(recordsRaw, BlockRecordRaw{Type: RecordTypeUsername, Data: []byte(user.Name)})

	return recordsRaw, nil
}

// decodeBlockRecordFiles decodes only file records. Other records are ignored
func decodeBlockRecordFiles(recordsRaw []BlockRecordRaw) (files []BlockRecordFile, err error) {
	for i, record := range recordsRaw {
		switch record.Type {
		case RecordTypeFile:
			if len(record.Data) < 61 {
				return nil, errors.New("decodeBlockRecordFiles file record invalid size")
			}

			file := BlockRecordFile{}
			file.Hash = make([]byte, hashSize)
			copy(file.Hash, record.Data[0:0+hashSize])
			copy(file.ID[:], record.Data[32:32+16])
			file.Type = record.Data[48]
			file.Format = binary.LittleEndian.Uint16(record.Data[49 : 49+2])
			file.Size = binary.LittleEndian.Uint64(record.Data[51 : 51+8])

			countTags := binary.LittleEndian.Uint16(record.Data[59 : 59+2])

			index := 61

			for n := uint16(0); n < countTags; n++ {
				if index+6 > len(record.Data) {
					return nil, errors.New("decodeBlockRecordFiles file record tags invalid size")
				}

				tag := BlockRecordFileTag{}
				tag.Type = binary.LittleEndian.Uint16(record.Data[index:index+2]) & 0x7FFF
				tagSize := binary.LittleEndian.Uint32(record.Data[index+2 : index+2+4])
				isDataReference := record.Data[index+1]&0x80 != 0

				if index+6+int(tagSize) > len(record.Data) {
					return nil, errors.New("decodeBlockRecordFiles file record tag data invalid size")
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
						return nil, errors.New("decodeBlockRecordFiles file record tag reference invalid size")
					}

					if refRecordNumber < 0 || refRecordNumber >= len(recordsRaw) {
						return nil, errors.New("decodeBlockRecordFiles file record tag reference not available")
					} else if recordsRaw[refRecordNumber].Type != RecordTypeTagData {
						return nil, errors.New("decodeBlockRecordFiles file record tag reference invalid")
					}

					tag.Data = recordsRaw[refRecordNumber].Data

				} else {
					tag.Data = record.Data[index+6 : index+6+int(tagSize)]
				}

				file.TagsRaw = append(file.TagsRaw, tag)

				if decoded, err := decodeFileTag(tag); err != nil {
					return nil, err
				} else if decoded != nil {
					file.TagsDecoded = append(file.TagsDecoded, decoded)
				}

				index += 6 + int(tagSize)
			}

			files = append(files, file)
		}
	}

	return files, err
}

// decodeFileTag decodes a file tag. If the tag type is not known, it returns nil.
func decodeFileTag(tag BlockRecordFileTag) (decoded interface{}, err error) {
	switch tag.Type {
	case TagTypeFolder:
		return FileTagFolder{Name: string(tag.Data)}, nil

	case TagTypeName:
		return FileTagName{Name: string(tag.Data)}, nil

	case TagTypeDescription:
		return FileTagDescription{Description: string(tag.Data)}, nil

	case TagTypeDateCreated:
		if len(tag.Data) != 8 {
			return nil, errors.New("decodeFileTag invalid date tag size")
		}

		timeB := int64(binary.LittleEndian.Uint64(tag.Data[0:8]))
		return FileTagCreated{Date: time.Unix(timeB, 0)}, nil

	}

	return nil, nil
}

// encodeFileTag encodes a file tag. If the tag type is not known, it returns nil.
func encodeFileTag(decoded interface{}) (tag BlockRecordFileTag, err error) {
	switch v := decoded.(type) {
	case FileTagFolder:
		return BlockRecordFileTag{Type: TagTypeFolder, Data: []byte(v.Name)}, nil

	case FileTagName:
		return BlockRecordFileTag{Type: TagTypeName, Data: []byte(v.Name)}, nil

	case FileTagDescription:
		return BlockRecordFileTag{Type: TagTypeDescription, Data: []byte(v.Description)}, nil

	case FileTagCreated:
		var tempDate [8]byte
		binary.LittleEndian.PutUint64(tempDate[0:8], uint64(v.Date.UTC().Unix()))
		return BlockRecordFileTag{Type: TagTypeDateCreated, Data: tempDate[:]}, nil

	}

	return tag, errors.New("encodeFileTag unknown tag type")
}

// encodeBlockRecordFiles encodes files into the block record data
// This function should be called grouped with all files in the same folder. The folder name is deduplicated; only unique folder records will be returned.
// Note that this function only stores the folder names as tags; it does not create separate TypeFolder file records.
func encodeBlockRecordFiles(files []BlockRecordFile) (recordsRaw []BlockRecordRaw, err error) {
	uniqueTagDataMap := make(map[string]struct{})
	duplicateTagDataMap := make(map[string]int) // list of tag data that appeared twice. Number in recordsRaw.

	// loop through all tags to encode them and create list of duplicates that will be replaced by references
	for n := range files {
		for m := range files[n].TagsDecoded {
			tag, err := encodeFileTag(files[n].TagsDecoded[m])
			if err != nil {
				return nil, err
			}
			files[n].TagsRaw = append(files[n].TagsRaw, tag)

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
		data := make([]byte, 61)

		if len(files[n].Hash) != hashSize {
			return nil, errors.New("encodeBlockRecords invalid file hash")
		}

		copy(data[0:32], files[n].Hash[0:32])
		copy(data[32:32+16], files[n].ID[:])

		data[48] = files[n].Type
		binary.LittleEndian.PutUint16(data[49:49+2], files[n].Format)
		binary.LittleEndian.PutUint64(data[51:51+8], files[n].Size)
		binary.LittleEndian.PutUint16(data[59:59+2], uint16(len(files[n].TagsRaw)))

		for _, tagRaw := range files[n].TagsRaw {
			if len(tagRaw.Data) > 4 {
				if refNumber, ok := duplicateTagDataMap[string(tagRaw.Data)]; ok {
					// In case the data is duplicated, use reference to the RecordTypeTagData instead
					tagRaw.Type |= 0x8000
					tagRaw.Data = intToBytes(-(len(recordsRaw) - refNumber))
				}
			}

			var tempTag [6]byte

			binary.LittleEndian.PutUint16(tempTag[0:2], tagRaw.Type)
			binary.LittleEndian.PutUint32(tempTag[2:2+4], uint32(len(tagRaw.Data)))

			data = append(data, tempTag[:]...)
			data = append(data, tagRaw.Data...)
		}

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

// TagRawToText returns the tag text of the given tag, if available. Empty if not.
func (file *BlockRecordFile) TagRawToText(tagType uint16) string {
	for m := range file.TagsRaw {
		if file.TagsRaw[m].Type == tagType {
			return string(file.TagsRaw[m].Data)
		}
	}

	return ""
}

// ---- high-level decoding ----

// BlockDecoded contains the decoded records from a block
type BlockDecoded struct {
	Block
	RecordsDecoded []interface{} // Decoded records. See BlockRecordX structures.
}

// decodeBlockRecords decodes all raw records in the block and returns a high-level decoded structure
// Use decodeBlockRecordX instead for specific record decoding.
func decodeBlockRecords(block *Block) (decoded *BlockDecoded, err error) {
	decoded = &BlockDecoded{Block: *block}

	files, err := decodeBlockRecordFiles(block.RecordsRaw)
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		decoded.RecordsDecoded = append(decoded.RecordsDecoded, file)
	}

	if user := decodeBlockRecordUser(block.RecordsRaw); user != nil {
		decoded.RecordsDecoded = append(decoded.RecordsDecoded, user)
	}

	return decoded, nil
}