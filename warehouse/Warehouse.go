/*
File Name:  Warehouse.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package warehouse

import (
	"encoding/hex"
	"io/ioutil"
	"os"
	"path/filepath"
)

// Blake3 hash size = 32 bytes.
const (
	hashSize = 256 / 8
)

// Warehouse represents a folder on disk.
type Warehouse struct {
	Directory string // The main directory for the files
	Temp      string // Temporary folder
}

// LogError is called for any error. The caller can use it to capture any errors.
//var LogError func(function, format string, v ...interface{}) = func(function, format string, v ...interface{}) {}

// Init initializes the warehouse
func Init(Directory string) (wh *Warehouse, err error) {
	// The temp folder will always be a sub-folder named "_Temp"
	wh = &Warehouse{Directory: Directory, Temp: filepath.Join(Directory, "_Temp")}

	if err = createDirectory(wh.Directory); err != nil {
		return nil, err
	}
	if err = createDirectory(wh.Temp); err != nil {
		return nil, err
	}

	return
}

// TempFile creates a temporary file in the Warehouse. Do not forget to delete.
func (wh *Warehouse) TempFile() (file *os.File, err error) {
	file, err = ioutil.TempFile(wh.Temp, "wh")

	return
}

// createFilePath creates the file path for the specified hash and returns the full file path
func (wh *Warehouse) createFilePath(hash string) (pathFull string, err error) {
	path, filename := buildPath(wh.Directory, hash)
	return filepath.Join(path, filename), createDirectory(path)
}

// FileExists checks if the file exists
func (wh *Warehouse) FileExists(hash string) (path string, fileInfo os.FileInfo, valid bool) {
	a, b := buildPath(wh.Directory, hash)
	path = filepath.Join(a, b)

	if fileInfo, err := os.Stat(path); err == nil {
		// file exists
		return path, fileInfo, true
	}

	return "", nil, false
}

// ---- hash functions ----

func ValidateHash(hash []byte) (hashA string, err error) {
	if len(hash) != hashSize {
		return "", os.ErrInvalid
	}
	return hex.EncodeToString(hash), nil
}

// ---- path ----

func createDirectory(path string) (err error) {
	if _, err = os.Stat(path); err != nil && os.IsNotExist(err) {
		err = os.MkdirAll(path, os.ModePerm)
	}
	return err
}

// buildPath returns the full directory and the filename for the hash
func buildPath(storagePath, hash string) (directory string, filename string) {
	part1 := hash[:4]
	part2 := hash[4:8]
	filename = hash[8:]

	newPath := filepath.Join(storagePath, part1, part2)

	return newPath, filename
}
