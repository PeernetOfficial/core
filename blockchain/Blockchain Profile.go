/*
File Name:  Blockchain Profile.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package blockchain

// ProfileReadField reads the specified profile field. See ProfileX for the list of recognized fields. The encoding depends on the field type. Status is BlockchainStatusX.
func (blockchain *Blockchain) ProfileReadField(index uint16) (data []byte, status int) {
	found := false

	status = blockchain.Iterate(func(block *Block) (statusI int) {
		fields, err := decodeBlockRecordProfile(block.RecordsRaw)
		if err != nil {
			return BlockchainStatusCorruptBlockRecord
		} else if len(fields) == 0 {
			return BlockchainStatusOK
		}

		// Check if the field is available in the profile record. If there are multiple records, only return the latest one.
		for n := range fields {
			if fields[n].Type == index {
				data = fields[n].Data
				found = true
			}
		}

		return BlockchainStatusOK
	})

	if status != BlockchainStatusOK {
		return nil, status
	} else if !found {
		return nil, BlockchainStatusDataNotFound
	}

	return data, BlockchainStatusOK
}

// ProfileList lists all profile fields. Status is BlockchainStatusX.
func (blockchain *Blockchain) ProfileList() (fields []BlockRecordProfile, status int) {
	uniqueFields := make(map[uint16][]byte)

	status = blockchain.Iterate(func(block *Block) (statusI int) {
		fields, err := decodeBlockRecordProfile(block.RecordsRaw)
		if err != nil {
			return BlockchainStatusCorruptBlockRecord
		}

		for n := range fields {
			uniqueFields[fields[n].Type] = fields[n].Data
		}

		return BlockchainStatusOK
	})

	for key, value := range uniqueFields {
		fields = append(fields, BlockRecordProfile{Type: key, Data: value})
	}

	return fields, status
}

// ProfileWrite writes profile fields and blobs to the blockchain. Status is BlockchainStatusX.
func (blockchain *Blockchain) ProfileWrite(fields []BlockRecordProfile) (newHeight, newVersion uint64, status int) {
	encoded, err := encodeBlockRecordProfile(fields)
	if err != nil {
		return 0, 0, BlockchainStatusCorruptBlockRecord
	}

	return blockchain.Append(encoded)
}

// ProfileDelete deletes fields and blobs from the blockchain. Status is BlockchainStatusX.
func (blockchain *Blockchain) ProfileDelete(fields []uint16) (newHeight, newVersion uint64, status int) {
	return blockchain.IterateDeleteRecord(func(record *BlockRecordRaw) (deleteAction int) {
		if record.Type != RecordTypeProfile {
			return 0 // no action
		}

		existingFields, err := decodeBlockRecordProfile([]BlockRecordRaw{*record})
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
