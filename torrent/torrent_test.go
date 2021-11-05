package torrent

import (
	"errors"
	"fmt"
	"github.com/PeernetOfficial/core/protocol"
	"testing"
)

// TestSplit ensures the split function is happening as required
func TestSplit(t *testing.T) {
	// Splitting Test file with each of 100 kb
	output, err := Split("TestingFiles/test.txt", 0.01,"TestingFiles/output/")
	if err != nil {
		fmt.Println(err)
		t.Fail()
	}

	PrettyPrint(output)
}

// Run TestSplit test before this no ensure the lime.epub-hashes file is generated
// TestReadHashes displays the output of the ReadHashes function
func TestReadHashes(t *testing.T) {
	hashes, err := ReadHashes("TestingFiles/output/test.txt-hashes.txt")
	if err != nil {
		fmt.Println(err)
		t.Fail()
	}
	PrettyPrint(hashes)
}

// TestFileChunks_Join Joining all chunks into a single file
func TestFileChunks_Join(t *testing.T) {
	hashes, err := ReadHashes("TestingFiles/output/test.txt-hashes.txt")
	if err != nil {
		fmt.Println(err)
		t.Fail()
	}

	// Joining all the chunks into a single file
	err = hashes.Join()
	if err != nil {
		fmt.Println(err)
		t.Fail()
	}

	// To ensure the join is successful we compare the hashes of the lime.epub
	// in the TestingFile folder and the lime.epub in the TestingFile/output
	// directory
	CorrectHash := protocol.HashDataString([]byte("TestingFiles/test.txt"))
	JoinedFilesHash := protocol.HashDataString([]byte("TestingFiles/output/test.txt"))
	if CorrectHash != JoinedFilesHash {
		fmt.Println(errors.New("hashes do not match"))
		fmt.Println("Expected Hash: " + CorrectHash)
		fmt.Println("Result Hash: " + JoinedFilesHash)
		t.Fail()
	}
}
