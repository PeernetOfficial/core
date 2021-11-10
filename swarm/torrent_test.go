package swarm

import (
	"errors"
	"fmt"
	"github.com/PeernetOfficial/core/protocol"
	"io/ioutil"
	"os"
	"testing"
)

// get bytes from file
func GetBytesFromFile(path string) ([]byte, error) {
	Byte, err := os.Open(path)
	// if we os.Open returns an error then handle it
	if err != nil {
		return nil,err
	}

	// Byte.Close()

	byteValue, err := ioutil.ReadAll(Byte)
	if err != nil {
		return nil, err
	}

	return byteValue, nil
}

// TestSplit ensures the split function is happening as required
func TestSplit(t *testing.T) {
	// Splitting Test file with each of 100 kb
	output, err := Split("TestingFiles/lime.epub", 0.2,"TestingFiles/output/")
	if err != nil {
		fmt.Println(err)
		t.Fail()
	}

	PrettyPrint(output)
}

// Run TestSplit test before this no ensure the lime.epub-hashes file is generated
// TestReadHashes displays the output of the ReadHashes function
func TestReadHashes(t *testing.T) {
	hashes, err := ReadHashes("TestingFiles/output/lime.epub-hashes.txt")
	if err != nil {
		fmt.Println(err)
		t.Fail()
	}
	PrettyPrint(hashes)
}

// TestFileChunks_Join Joining all chunks into a single file
func TestFileChunks_Join(t *testing.T) {
	hashes, err := ReadHashes("TestingFiles/output/lime.epub-hashes.txt")
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

	// get the bytes from "TestingFiles/lime.epub"
	RightBytes, err := GetBytesFromFile("TestingFiles/lime.epub")
	if err != nil {
		fmt.Println(err)
		t.Fail()
	}

	// get the byte value for "TestingFiles//output/lime.epub"
	CheckBytes, err := GetBytesFromFile("TestingFiles/output/lime.epub")
	if err != nil {
		fmt.Println(err)
		t.Fail()
	}

	CorrectHash := protocol.HashDataString(RightBytes)
	JoinedFilesHash := protocol.HashDataString(CheckBytes)
	if CorrectHash != JoinedFilesHash {
		fmt.Println(errors.New("hashes do not match"))
		fmt.Println("Expected Hash: " + CorrectHash)
		fmt.Println("Result Hash: " + JoinedFilesHash)
		t.Fail()
	}
}
