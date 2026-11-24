//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

/*
File Name:  Lock File Unix.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"os"
	"syscall"
)

// lockFile opens the file, creating it if it does not exist, and takes an exclusive lock on it.
// The lock is held until the file is closed or the process ends.
// If the lock is held already, locked is true and err is the error of the operating system.
func lockFile(filename string) (file *os.File, locked bool, err error) {
	if file, err = os.OpenFile(filename, os.O_RDWR|os.O_CREATE, 0666); err != nil { // 666 : All uses can read/write
		return nil, false, err
	}

	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, err == syscall.EWOULDBLOCK, err
	}

	return file, false, nil
}
