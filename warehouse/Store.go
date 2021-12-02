/*
File Name:  Store.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package warehouse

import (
	"github.com/PeernetOfficial/core/search"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PeernetOfficial/core/merkle"
	"lukechampine.com/blake3"
)

const (
	StatusOK                  = 0  // Success.
	StatusErrorCreateTempFile = 1  // Error creating a temporary file.
	StatusErrorWriteTempFile  = 2  // Error writing temporary file.
	StatusErrorCloseTempFile  = 3  // Error closing temporary file.
	StatusErrorRenameTempFile = 4  // Error renaming temporary file.
	StatusErrorCreatePath     = 5  // Error creating path for target file in warehouse.
	StatusErrorOpenFile       = 7  // Error opening file.
	StatusInvalidHash         = 8  // Invalid hash.
	StatusFileNotFound        = 9  // File not found.
	StatusErrorDeleteFile     = 10 // Error deleting file.
	StatusErrorReadFile       = 11 // Error reading file.
	StatusErrorSeekFile       = 12 // Error seeking to position in file.
	StatusErrorTargetExists   = 13 // Target file already exists.
	StatusErrorCreateTarget   = 14 // Error creating target file.
	StatusErrorCreateMerkle   = 15 // Error creating merkle tree.
	StatusErrorMerkleTreeFile = 16 // Invalid merkle tree companion file.
)

// CreateFile creates a new file in the warehouse
// If fileSize is provided, creating the merkle tree is significantly faster as it will be created on the fly. If the file size is unknown, set the size to 0.
func (wh *Warehouse) CreateFile(data io.Reader, fileSize uint64) (hash []byte, status int, err error) {
	// create a temporary file to hold the body content
	tmpFile, err := wh.tempFile()
	if err != nil {
		return nil, StatusErrorCreateTempFile, err
	}

	tmpFileName := tmpFile.Name()
	// generate search index for the file
	_, err = search.GenerateIndexes(tmpFileName)
	if err != nil {
		return nil, 0, err
	}

	// create merkle tree in parallel if the file size is known (which means the fragment size can be calculated)
	if fileSize > 0 {
		// TODO
	}

	// create the hash-writer
	hashWriter := blake3.New(hashSize, nil)

	// the multi-writer writes to the temp-file and the hash simultaneously
	mw := io.MultiWriter(tmpFile, hashWriter)

	// copy into the multiwriter
	if _, err = io.Copy(mw, data); err != nil {
		tmpFile.Close()
		os.Remove(tmpFileName)
		return nil, StatusErrorWriteTempFile, err
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpFileName)
		return nil, StatusErrorCloseTempFile, err
	}

	hash = hashWriter.Sum(nil)

	// Check if the file exists
	if _, _, status, _ := wh.FileExists(hash); status == StatusOK {
		// file exists already, temp file not needed
		os.Remove(tmpFileName)

		// return success
		return hash, StatusOK, nil
	}

	// Destination
	pathFull, err := wh.createFilePath(hash)
	if err != nil {
		os.Remove(tmpFileName)
		return nil, StatusErrorCreatePath, err
	}

	// first check if the file is already stored. if not rename the temp file to the final one
	if _, err := os.Stat(pathFull); err == nil {
		// file exists already, temp file not needed
		os.Remove(tmpFileName)
	} else {
		// rename temp file to final one with proper path
		if err := os.Rename(tmpFileName, pathFull); err != nil {
			os.Remove(tmpFileName)

			// A race condition may exist where the file exists here. If it does, continue successfully.
			if _, err = os.Stat(pathFull); err != nil {
				return nil, StatusErrorRenameTempFile, err
			}
		}

		// create the merkle tree companion file
		if fileSize == 0 || fileSize > merkle.MinimumFragmentSize {
			if status, err = wh.createMerkleCompanionFile(pathFull); status != StatusOK {
				return hash, status, err
			}
		}
	}

	return hash, StatusOK, nil
}

// CreateFileFromPath creates a file from an existing file path.
// Warning: An attacker could supply any local file using this function, put them into storage and read them! No input path verification or limitation is done.
func (wh *Warehouse) CreateFileFromPath(file string) (hash []byte, status int, err error) {
	fileHandle, err := os.Open(file)
	if err != nil && os.IsNotExist(err) {
		return nil, StatusFileNotFound, err
	} else if err != nil {
		// cannot open file
		return nil, StatusErrorOpenFile, err
	}
	defer fileHandle.Close()

	var fileSize uint64
	if stat, err := fileHandle.Stat(); err == nil {
		fileSize = uint64(stat.Size())
	}

	// create the file using the opened file
	return wh.CreateFile(fileHandle, fileSize)
}

// ReadFile reads a file from the warehouse and outputs it to the writer
// Offset is the position in the file to start reading. Limit (0 = not used) defines how many bytes to read starting at the offset.
// Return status codes: StatusInvalidHash, StatusFileNotFound, StatusErrorOpenFile, StatusErrorSeekFile, StatusErrorReadFile, StatusOK
func (wh *Warehouse) ReadFile(hash []byte, offset, limit int64, writer io.Writer) (status int, bytesRead int64, err error) {
	path, _, status, err := wh.FileExists(hash)
	if status != StatusOK { // file does not exist or invalid hash
		return status, 0, err
	}

	// read the file from disk
	var reader io.ReadSeeker
	retryCount := 0
retryOpenFile:

	file, err := os.Open(path)
	if err != nil {
		// There may be a race condition when the file is being written: "The process cannot access the file because it is being used by another process."
		// Wait up to 3 times for 400ms.
		if strings.Contains(err.Error(), "cannot access the file because it is being used by another process") && retryCount < 3 {
			retryCount++
			time.Sleep(time.Millisecond * 400)
			goto retryOpenFile
		}

		return StatusErrorOpenFile, 0, err
	}
	defer file.Close()

	reader = file

	// seek to offset, if provided
	if offset > 0 {
		if _, err = reader.Seek(offset, io.SeekStart); err != nil {
			return StatusErrorSeekFile, 0, err
		}
	}

	// read the file and copy it into the output
	if limit > 0 {
		bytesRead, err = io.Copy(writer, io.LimitReader(reader, limit))
	} else {
		bytesRead, err = io.Copy(writer, reader)
	}

	// do not consider EOF an error if all bytes were read
	if err != nil {
		return StatusErrorReadFile, bytesRead, err
	}

	return StatusOK, bytesRead, nil
}

// DeleteFile deletes a file from the warehouse
func (wh *Warehouse) DeleteFile(hash []byte) (status int, err error) {
	path, _, status, err := wh.FileExists(hash)
	if status != StatusOK {
		return status, err
	}

	if err := os.Remove(path); err != nil {
		return StatusErrorDeleteFile, err
	}

	// Remove file generated indexes
	err = search.RemoveIndexesHash(hash)
	if err != nil {
		return 0, err
	}

	return StatusOK, nil
}

// FileExists checks if the file exists. It returns StatusInvalidHash, StatusFileNotFound, or StatusOK.
func (wh *Warehouse) FileExists(hash []byte) (path string, fileInfo os.FileInfo, status int, err error) {
	hashA, err := ValidateHash(hash)
	if err != nil {
		return "", nil, StatusInvalidHash, err
	}

	a, b := buildPath(wh.Directory, hashA)
	path = filepath.Join(a, b)

	if fileInfo, err := os.Stat(path); err == nil {
		// file exists
		return path, fileInfo, StatusOK, nil
	}

	return "", nil, StatusFileNotFound, os.ErrNotExist
}

// DeleteWarehouse deletes all files in the warehouse
func (wh *Warehouse) DeleteWarehouse() (err error) {
	return wh.IterateFiles(func(Hash []byte, Size int64) (Continue bool) {
		wh.DeleteFile(Hash)

		return true
	})
}

// ReadFileToDisk reads a file from the warehouse and outputs it to the target file. The function fails with StatusErrorTargetExists if the target file already exists.
// Offset is the position in the file to start reading. Limit (0 = not used) defines how many bytes to read starting at the offset.
// Return status codes: StatusInvalidHash, StatusFileNotFound, StatusErrorTargetExists, StatusErrorCreateTarget, StatusErrorOpenFile, StatusErrorSeekFile, StatusErrorReadFile, StatusOK
func (wh *Warehouse) ReadFileToDisk(hash []byte, offset, limit int64, fileTarget string) (status int, bytesRead int64, err error) {
	// check if the target file already exist
	if _, err := os.Stat(fileTarget); err == nil {
		return StatusErrorTargetExists, 0, nil
	}

	// create the target file
	fileT, err := os.OpenFile(fileTarget, os.O_WRONLY|os.O_CREATE, 0666) // 666 = All uses can read/write
	if err != nil {
		return StatusErrorCreateTarget, 0, err
	}
	defer fileT.Close()

	return wh.ReadFile(hash, offset, limit, fileT)
}
