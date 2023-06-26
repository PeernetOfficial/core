/*
File Username:  Profile.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package blockchain

import "fmt"

// ProfileReadField reads the specified profile field. See ProfileX for the list of recognized fields. The encoding depends on the field type. Status is StatusX.
func (blockchain *Blockchain) ProfileReadField(index uint16) (data []byte, status int) {
	found := false

	status = blockchain.Iterate(func(block *Block) (statusI int) {
		fields, err := DecodeBlockRecordProfile(block.RecordsRaw)
		if err != nil {
			return StatusCorruptBlockRecord
		} else if len(fields) == 0 {
			return StatusOK
		}

		// Check if the field is available in the profile record. If there are multiple records, only return the latest one.
		for n := range fields {
			if fields[n].Type == index {
				data = fields[n].Data
				found = true
			}
		}

		return StatusOK
	})

	if status != StatusOK {
		return nil, status
	} else if !found {
		return nil, StatusDataNotFound
	}

	return data, StatusOK
}

// ProfileList lists all profile fields. Status is StatusX.
func (blockchain *Blockchain) ProfileList() (fields []BlockRecordProfile, status int) {
	uniqueFields := make(map[uint16][]byte)

	fmt.Println(blockchain.height)

	status = blockchain.Iterate(func(block *Block) (statusI int) {
		fields, err := DecodeBlockRecordProfile(block.RecordsRaw)
		if err != nil {
			return StatusCorruptBlockRecord
		}

		for n := range fields {
			uniqueFields[fields[n].Type] = fields[n].Data
		}

		return StatusOK
	})

	for key, value := range uniqueFields {
		fields = append(fields, BlockRecordProfile{Type: key, Data: value})
	}

	return fields, status
}

// ProfileWrite writes profile fields and blobs to the blockchain. Status is StatusX.
func (blockchain *Blockchain) ProfileWrite(fields []BlockRecordProfile) (newHeight, newVersion uint64, status int) {
	encodeProfileAppend := func(fields []BlockRecordProfile) (newHeight, newVersion uint64, status int) {
		encoded, err := encodeBlockRecordProfile(fields)
		if err != nil {
			return 0, 0, StatusCorruptBlockRecord
		}

		return blockchain.Append(encoded)
	}

	blockSize := uint64(blockHeaderSize)
	var recordFields []BlockRecordProfile

	for _, field := range fields {
		recordSize := field.SizeInBlock()

		// need to create a new block due to target block size?
		if len(recordFields) > 0 && blockSize+recordSize > TargetBlockSize {
			if newHeight, newVersion, status = encodeProfileAppend(recordFields); status != StatusOK {
				return newHeight, newVersion, status
			}

			blockSize = blockHeaderSize
			recordFields = nil
		}

		blockSize += recordSize
		recordFields = append(recordFields, field)
	}

	return encodeProfileAppend(recordFields)
}

// ProfileDelete deletes fields and blobs from the blockchain. Status is StatusX.
func (blockchain *Blockchain) ProfileDelete(fields []uint16) (newHeight, newVersion uint64, status int) {
	return blockchain.IterateDeleteRecord(nil, func(record *BlockRecordRaw) (deleteAction int) {
		if record.Type != RecordTypeProfile {
			return 0 // no action
		}

		existingFields, err := DecodeBlockRecordProfile([]BlockRecordRaw{*record})
		if err != nil || len(existingFields) != 1 {
			return 3 // error blockchain corrupt
		}

		for _, i := range fields {
			if i == existingFields[0].Type { // found a file ID to delete?
				return 1 // delete record
			}
		}

		return 0 // no action on record
	})
}
