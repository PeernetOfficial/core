/*
File Username:  Merkle.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package warehouse

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/PeernetOfficial/core/merkle"
)

// Merkle companion files are created to store the entire merkle tree for files that are bigger than the minimum fragment size.
const merkleCompanionExt = ".merkle"

// MerkleFileExists checks if the merkle companion file exists. It returns StatusInvalidHash, StatusFileNotFound, or StatusOK.
func (wh *Warehouse) MerkleFileExists(hash []byte) (path string, fileSize uint64, status int, err error) {
	hashA, err := ValidateHash(hash)
	if err != nil {
		return "", 0, StatusInvalidHash, err
	}

	a, b := buildPath(wh.Directory, hashA)
	path = filepath.Join(a, b) + merkleCompanionExt

	if fileInfo, err := os.Stat(path); err == nil {
		// file exists
		return path, uint64(fileInfo.Size()), StatusOK, nil
	}

	return "", 0, StatusFileNotFound, os.ErrNotExist
}

// createMerkleCompanionFile creates a merkle companion file. If the merkle companion file already exists, it is overwritten.
// dataFilePath is the full path to the data file in the warehouse.
func (wh *Warehouse) createMerkleCompanionFile(dataFilePath string) (status int, err error) {
	// open the data file
	dataFile, err := os.Open(dataFilePath)
	if err != nil && os.IsNotExist(err) {
		return StatusFileNotFound, err
	} else if err != nil {
		return StatusErrorOpenFile, err
	}
	defer dataFile.Close()

	var fileSize uint64
	stat, err := dataFile.Stat()
	if err != nil {
		return StatusErrorOpenFile, err
	}
	fileSize = uint64(stat.Size())

	// Merkle files are only created if merkle trees are actually used, which means the file must be bigger than the minimum fragment size.
	// Otherwise the merkle root hash will be just the file hash and the merkle overhead provides no advantage whatsoever.
	if fileSize <= merkle.MinimumFragmentSize {
		return StatusOK, nil
	}

	// Create a new merkle file. If one exists, overwrite.
	merkleFile := dataFilePath + merkleCompanionExt

	fileM, err := os.OpenFile(merkleFile, os.O_WRONLY|os.O_CREATE, 0666) // 666 = All uses can read/write
	if err != nil {
		return StatusErrorCreateTarget, err
	}
	defer fileM.Close()

	// create the merkle tree and write it to the companion file
	fragmentSize := merkle.CalculateFragmentSize(fileSize)
	tree, err := merkle.NewMerkleTree(fileSize, fragmentSize, dataFile)
	if err != nil {
		return StatusErrorCreateMerkle, err
	}

	fileM.Write(tree.Export())

	return StatusOK, nil
}

// ReadMerkleTree reads the merkle tree from the companion file associated with the hash.
// It is the callers responsibility to first check if a merkle tree file is to be expected (files smaller or equal than the minimum fragment size do not use a merkle tree).
func (wh *Warehouse) ReadMerkleTree(hash []byte, headerOnly bool) (tree *merkle.MerkleTree, status int, err error) {
	path, _, status, err := wh.MerkleFileExists(hash)
	if status != StatusOK { // file does not exist or invalid hash
		return nil, status, err
	}

	fileM, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, StatusErrorOpenFile, err
	}
	defer fileM.Close()

	if headerOnly {
		data := make([]byte, merkle.MerkleTreeFileHeaderSize)
		if _, err = io.ReadFull(fileM, data); err != nil {
			return nil, StatusErrorReadFile, err
		}

		if tree = merkle.ReadMerkleTreeHeader(data); tree == nil {
			return nil, StatusErrorMerkleTreeFile, errors.New("invalid merkle tree file header")
		}
	} else {
		data, err := io.ReadAll(fileM)
		if err != nil {
			return nil, StatusErrorReadFile, err
		}

		if tree = merkle.ImportMerkleTree(data); tree == nil {
			return nil, StatusErrorMerkleTreeFile, errors.New("invalid merkle tree file header")
		}
	}

	return tree, StatusOK, nil
}
