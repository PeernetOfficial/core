/*
File Name:  File.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Note that files include virtual folders as well for any operation.
*/

package blockchain

import (
	"bytes"

	"github.com/google/uuid"
)

// AddFiles adds files to the blockchain. Status is StatusX.
// It makes sense to group all files in the same directory into one call, since only one directory record will be created per unique directory per block.
func (blockchain *Blockchain) AddFiles(files []BlockRecordFile) (newHeight, newVersion uint64, status int) {
	encoded, err := encodeBlockRecordFiles(files)
	if err != nil {
		return 0, 0, StatusCorruptBlockRecord
	}

	return blockchain.Append(encoded)
}

// ListFiles returns a list of all files. Status is StatusX.
// If there is a corruption in the blockchain it will stop reading but return the files parsed so far.
func (blockchain *Blockchain) ListFiles() (files []BlockRecordFile, status int) {
	status = blockchain.Iterate(func(block *Block) (statusI int) {
		filesMore, err := decodeBlockRecordFiles(block.RecordsRaw, block.NodeID)
		if err != nil {
			return StatusCorruptBlockRecord
		}
		files = append(files, filesMore...)

		return StatusOK
	})

	return files, status
}

// FileExists checks if the file (identified via its hash) exists.
// If there is a corruption in the blockchain it will stop reading but return the files found so far.
func (blockchain *Blockchain) FileExists(hash []byte) (files []BlockRecordFile, status int) {
	status = blockchain.Iterate(func(block *Block) (statusI int) {
		filesD, err := decodeBlockRecordFiles(block.RecordsRaw, block.NodeID)
		if err != nil {
			return StatusCorruptBlockRecord
		}
		for _, file := range filesD {
			if bytes.Equal(file.Hash, hash) {
				files = append(files, file)
			}
		}

		return StatusOK
	})

	return files, status
}

// DeleteFiles deletes files from the blockchain. Status is StatusX.
func (blockchain *Blockchain) DeleteFiles(IDs []uuid.UUID) (newHeight, newVersion uint64, deletedFiles []*BlockRecordFile, status int) {
	newHeight, newVersion, status = blockchain.IterateDeleteRecord(func(file *BlockRecordFile) (deleteAction int) {
		for _, id := range IDs {
			if file.ID == id { // found a file ID to delete?
				deletedFiles = append(deletedFiles, file)
				return 1 // delete record
			}
		}

		return 0 // no action on record
	}, nil)

	return
}

// ReplaceFiles is a convenience wrapper to replace files in the blockchain identified via their IDs. Status is StatusX.
// If a file does not exist on the blockchain, it acts as add.
func (blockchain *Blockchain) ReplaceFiles(files []BlockRecordFile) (newHeight, newVersion uint64, status int) {
	var deleteIDs []uuid.UUID
	for n := range files {
		deleteIDs = append(deleteIDs, files[n].ID)
	}

	if newHeight, newVersion, _, status = blockchain.DeleteFiles(deleteIDs); status != StatusOK {
		return
	}

	return blockchain.AddFiles(files)
}
