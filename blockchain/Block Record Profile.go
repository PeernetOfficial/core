/*
File Username:  Block Record Profile.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Profile records:
Offset  Size    Info
0       2       Type
2       ?       Data according to the type

*/

package blockchain

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// BlockRecordProfile provides information about the end user.
type BlockRecordProfile struct {
	Type uint16 // See ProfileX constants.
	Data []byte // Data
}

// DecodeBlockRecordProfile decodes only profile records. Other records are ignored.
func DecodeBlockRecordProfile(recordsRaw []BlockRecordRaw) (fields []BlockRecordProfile, err error) {
	fieldMap := make(map[uint16][]byte)

	for _, record := range recordsRaw {
		if record.Type != RecordTypeProfile {
			continue
		}

		if len(record.Data) < 2 {
			return nil, errors.New("profile record invalid size")
		}

		fieldType := binary.LittleEndian.Uint16(record.Data[0:2])
		fmt.Println(record.Data[0:2])
		fmt.Println(fieldType)
		fieldMap[fieldType] = record.Data[2:]
	}

	for fieldType, fieldData := range fieldMap {
		fmt.Println("type value added")
		fmt.Println(fieldType)
		fields = append(fields, BlockRecordProfile{Type: fieldType, Data: fieldData})
	}

	return fields, nil
}

// encodeBlockRecordProfile encodes the profile record.
func encodeBlockRecordProfile(fields []BlockRecordProfile) (recordsRaw []BlockRecordRaw, err error) {
	if len(fields) > math.MaxUint16 {
		return nil, errors.New("exceeding max count of fields")
	}

	for n := range fields {
		if uint64(len(fields[n].Data)) > math.MaxUint32 {
			return nil, errors.New("exceeding max field size")
		}

		data := make([]byte, 2)
		binary.LittleEndian.PutUint16(data[0:2], fields[n].Type)
		data = append(data, fields[n].Data...)

		recordsRaw = append(recordsRaw, BlockRecordRaw{Type: RecordTypeProfile, Data: data})
	}

	return recordsRaw, nil
}

// SizeInBlock returns the full size this file takes up in a single block. (i.e., the record size)
func (field *BlockRecordProfile) SizeInBlock() (size uint64) {
	return blockRecordHeaderSize + 2 + uint64(len(field.Data))
}
