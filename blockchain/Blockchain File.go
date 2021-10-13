/*
File Name:  Blockchain File.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package blockchain

import (
	"bytes"

	"github.com/google/uuid"
)

// AddFiles adds files to the blockchain. Status is BlockchainStatusX.
// It makes sense to group all files in the same directory into one call, since only one directory record will be created per unique directory per block.
func (blockchain *Blockchain) AddFiles(files []BlockRecordFile) (newHeight, newVersion uint64, status int) {
	encoded, err := encodeBlockRecordFiles(files)
	if err != nil {
		return 0, 0, BlockchainStatusCorruptBlockRecord
	}

	return blockchain.Append(encoded)
}

// ListFiles returns a list of all files. Status is BlockchainStatusX.
// If there is a corruption in the blockchain it will stop reading but return the files parsed so far.
func (blockchain *Blockchain) ListFiles() (files []BlockRecordFile, status int) {
	status = blockchain.Iterate(func(block *Block) (statusI int) {
		filesMore, err := decodeBlockRecordFiles(block.RecordsRaw, block.NodeID)
		if err != nil {
			return BlockchainStatusCorruptBlockRecord
		}
		files = append(files, filesMore...)

		return BlockchainStatusOK
	})

	return files, status
}

// FileExists checks if the file (identified via its hash) exists.
// If there is a corruption in the blockchain it will stop reading but return the files found so far.
func (blockchain *Blockchain) FileExists(hash []byte) (files []BlockRecordFile, status int) {
	status = blockchain.Iterate(func(block *Block) (statusI int) {
		filesD, err := decodeBlockRecordFiles(block.RecordsRaw, block.NodeID)
		if err != nil {
			return BlockchainStatusCorruptBlockRecord
		}
		for _, file := range filesD {
			if bytes.Equal(file.Hash, hash) {
				files = append(files, file)
			}
		}

		return BlockchainStatusOK
	})

	return files, status
}

// DeleteFiles deletes files from the blockchain. Status is BlockchainStatusX.
func (blockchain *Blockchain) DeleteFiles(IDs []uuid.UUID) (newHeight, newVersion uint64, deletedFiles []BlockRecordFile, status int) {
	newHeight, newVersion, status = blockchain.IterateDeleteRecord(func(record *BlockRecordRaw) (deleteAction int) {
		if record.Type != RecordTypeFile {
			return 0 // no action on record
		}

		filesDecoded, err := decodeBlockRecordFiles([]BlockRecordRaw{*record}, nil)
		if err != nil || len(filesDecoded) != 1 {
			return 3 // error blockchain corrupt
		}

		for _, id := range IDs {
			if id == filesDecoded[0].ID { // found a file ID to delete?
				deletedFiles = append(deletedFiles, filesDecoded[0])
				return 1 // delete record
			}
		}

		return 0 // no action on record
	})

	return
}
