//go:build windows

/*
File Name:  Lock File Windows.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile opens the file, creating it if it does not exist, and takes an exclusive lock on it.
// The lock is held until the file is closed or the process ends.
// If the lock is held already, locked is true and err is the error of the operating system.
func lockFile(filename string) (file *os.File, locked bool, err error) {
	if file, err = os.OpenFile(filename, os.O_RDWR|os.O_CREATE, 0666); err != nil { // 666 : All uses can read/write
		return nil, false, err
	}

	// The lock covers the entire file: the range starts at offset 0 of the overlapped structure and has the maximum length.
	err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, ^uint32(0), ^uint32(0), new(windows.Overlapped))
	if err != nil {
		file.Close()
		return nil, err == windows.ERROR_LOCK_VIOLATION, err
	}

	return file, false, nil
}
