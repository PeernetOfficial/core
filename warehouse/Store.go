/*
File Name:  Store.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package warehouse

import (
	"encoding/hex"
	"io"
	"os"
	"strings"
	"time"

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
)

// CreateFile creates a new file in the warehouse
func (wh *Warehouse) CreateFile(data io.Reader) (hash []byte, status int, err error) {
	// create a temporary file to hold the body content
	tmpFile, err := wh.TempFile()
	if err != nil {
		return nil, StatusErrorCreateTempFile, err
	}

	tmpFileName := tmpFile.Name()

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
	hashA := hex.EncodeToString(hash)

	// Check if the file exists
	if _, _, valid := wh.FileExists(hashA); valid {
		// file exists already, temp file not needed
		os.Remove(tmpFileName)

		// return success
		return hash, StatusOK, nil
	}

	// Destination
	pathFull, err := wh.createFilePath(hashA)
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

	// create the file using the opened file
	return wh.CreateFile(fileHandle)
}

// ReadFile reads a file from the warehouse and outputs it to the writer
// Offset is the position in the file to start reading. Limit (0 = not used) defines how many bytes to read starting at the offset.
func (wh *Warehouse) ReadFile(hash []byte, offset, limit int64, writer io.Writer) (status int, err error) {
	hashA, err := validateHash(hash)
	if err != nil {
		return StatusInvalidHash, err
	}

	var reader io.ReadSeeker

	path, _, valid := wh.FileExists(hashA)
	if !valid {
		// file does not exist
		return StatusFileNotFound, os.ErrNotExist
	}

	// read from drive
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

		return StatusErrorOpenFile, err
	}
	defer file.Close()

	reader = file

	// seek to offset, if provided
	if offset > 0 {
		if _, err = reader.Seek(offset, io.SeekStart); err != nil {
			return StatusErrorSeekFile, err
		}
	}

	// read the file and copy it into the output
	if limit > 0 {
		_, err = io.Copy(writer, io.LimitReader(reader, limit))
	} else {
		_, err = io.Copy(writer, reader)
	}

	// do not consider EOF an error if all bytes were read
	if err != nil {
		return StatusErrorReadFile, err
	}

	return StatusOK, nil
}

// DeleteFile deletes a file from the warehouse
func (wh *Warehouse) DeleteFile(hash []byte) (status int, err error) {
	hashA, err := validateHash(hash)
	if err != nil {
		return StatusInvalidHash, err
	}

	path, _, valid := wh.FileExists(hashA)
	if !valid {
		return StatusFileNotFound, os.ErrNotExist
	}

	if err := os.Remove(path); err != nil {
		return StatusErrorDeleteFile, err
	}

	return StatusOK, nil
}
