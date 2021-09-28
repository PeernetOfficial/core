/*
File Name:  File Metadata.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Metadata tags provide meta information about files.
*/

package core

import (
	"encoding/binary"
	"errors"
	"time"
)

// List of defined file tags.
const (
	TagName        = 0 // Name of file.
	TagFolder      = 1 // Folder name.
	TagDescription = 2 // Arbitrary description of the file. May contain hashtags.
	TagDateShared  = 3 // When the file was published on the blockchain. Cannot be set manually (virtual read-only tag).
	TagDateCreated = 4 // Date when the file was originally created. This may differ from the date in the block record, which indicates when the file was shared.
)

// Future tags to be defined for audio/video: Artist, Album, Title, Length, Bitrate, Codec
// Windows list: https://docs.microsoft.com/en-us/windows/win32/wmdm/metadata-constants

// ---- encoding ----

// Date returns the tags data as date encoded
func (tag *BlockRecordFileTag) Date() (time.Time, error) {
	if len(tag.Data) != 8 {
		return time.Time{}, errors.New("file tag date invalid size")
	}

	timeB := int64(binary.LittleEndian.Uint64(tag.Data[0:8]))
	return time.Unix(timeB, 0).UTC(), nil
}

// Text returns the tags data as text encoded
func (tag *BlockRecordFileTag) Text() string {
	return string(tag.Data)
}

// IsVirtual checks if the tag is virtual.
func (tag *BlockRecordFileTag) IsVirtual() bool {
	return tag.Type == TagDateShared
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
