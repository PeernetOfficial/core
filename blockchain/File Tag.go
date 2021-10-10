/*
File Name:  File Tag.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Metadata tags provide meta information about files.
*/

package blockchain

import (
	"encoding/binary"
	"errors"
	"time"
)

// List of defined file tags. Virtual tags are generated at runtime and are read-only. They cannot be stored on the blockchain.
const (
	TagName          = 0 // Name of file.
	TagFolder        = 1 // Folder name.
	TagDescription   = 2 // Arbitrary description of the file. May contain hashtags.
	TagDateShared    = 3 // When the file was published on the blockchain. Virtual.
	TagDateCreated   = 4 // Date when the file was originally created. This may differ from the date in the block record, which indicates when the file was shared.
	TagSharedByCount = 5 // Count of peers that share the file. Virtual.
	TagSharedByGeoIP = 6 // GeoIP data of peers that are sharing the file. CSV encoded with header "latitude,longitude". Virtual.
)

// Future tags to be defined for audio/video: Artist, Album, Title, Length, Bitrate, Codec
// Windows list: https://docs.microsoft.com/en-us/windows/win32/wmdm/metadata-constants

// ---- encoding ----

// Date returns the tags data as date encoded
func (tag *BlockRecordFileTag) Date() (time.Time, error) {
	if tag == nil {
		return time.Time{}, errors.New("tag not available")
	} else if len(tag.Data) != 8 {
		return time.Time{}, errors.New("file tag date invalid size")
	}

	timeB := int64(binary.LittleEndian.Uint64(tag.Data[0:8]))
	return time.Unix(timeB, 0).UTC(), nil
}

// Text returns the tags data as text encoded
func (tag *BlockRecordFileTag) Text() string {
	return string(tag.Data)
}

// Number returns the tags data as uint64. It returns 0 if the data cannot be decoded.
func (tag *BlockRecordFileTag) Number() uint64 {
	if len(tag.Data) != 8 {
		return 0
	}

	return binary.LittleEndian.Uint64(tag.Data[0:8])
}

// IsVirtual checks if the tag is virtual.
func (tag *BlockRecordFileTag) IsVirtual() bool {
	return IsTagVirtual(tag.Type)
}

// TagFromDate returns a tag from date
func TagFromDate(Type uint16, Date time.Time) BlockRecordFileTag {
	var tempDate [8]byte
	binary.LittleEndian.PutUint64(tempDate[0:8], uint64(Date.UTC().Unix()))

	return BlockRecordFileTag{Type: Type, Data: tempDate[:]}
}

// TagFromText returns a tag from text
func TagFromText(Type uint16, Text string) BlockRecordFileTag {
	return BlockRecordFileTag{Type: Type, Data: []byte(Text)}
}

// TagFromNumber returns a tag from a number
func TagFromNumber(Type uint16, Number uint64) BlockRecordFileTag {
	var tempDate [8]byte
	binary.LittleEndian.PutUint64(tempDate[0:8], Number)

	return BlockRecordFileTag{Type: Type, Data: tempDate[:]}
}

// IsTagVirtual checks if the tag is a virtual one.
func IsTagVirtual(Type uint16) bool {
	switch Type {
	case TagDateShared, TagSharedByCount, TagSharedByGeoIP:
		return true
	default:
		return false
	}
}

// GetTag returns the tag with the type or nil if not available.
func (file *BlockRecordFile) GetTag(Type uint16) (tag *BlockRecordFileTag) {
	for n := range file.Tags {
		if file.Tags[n].Type == Type {
			return &file.Tags[n]
		}
	}

	return nil
}
