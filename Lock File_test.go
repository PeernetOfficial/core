/*
File Name:  Lock File_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"path/filepath"
	"testing"
)

// TestLockFile takes the lock on a file, expects a second attempt to report it as locked, and expects the lock to be free again once the first file is closed.
func TestLockFile(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "Config.yaml.lock")

	first, locked, err := lockFile(filename)
	if err != nil || locked || first == nil {
		t.Fatalf("first lock: file %v, locked %v, error %v", first, locked, err)
	}

	second, locked, err := lockFile(filename)
	if !locked || err == nil || second != nil {
		if second != nil {
			second.Close()
		}
		first.Close()
		t.Fatalf("second lock while the first is held: file %v, locked %v, error %v", second, locked, err)
	}

	if err = first.Close(); err != nil {
		t.Fatalf("closing the first file: %v", err)
	}

	third, locked, err := lockFile(filename)
	if err != nil || locked || third == nil {
		t.Fatalf("lock after the first file was closed: file %v, locked %v, error %v", third, locked, err)
	}
	third.Close()
}

// TestLockFileError expects a file that cannot be opened to be reported as an error and not as locked.
func TestLockFileError(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "missing directory", "Config.yaml.lock")

	file, locked, err := lockFile(filename)
	if err == nil || locked || file != nil {
		if file != nil {
			file.Close()
		}
		t.Fatalf("lock in a directory that does not exist: file %v, locked %v, error %v", file, locked, err)
	}
}
