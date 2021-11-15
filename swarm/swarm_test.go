package swarm

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/PeernetOfficial/core/protocol"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"testing"
)


// Helper download file function
func downloadFile(filepath string, url string) error {
	// Create the file
	out, err := os.Create(filepath)
	if err != nil  {
		return err
	}
	defer out.Close()
	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Check server response
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}
	// Writer the body to file
	_, err = io.Copy(out, resp.Body)
	if err != nil  {
		return err
	}
	return nil
}

// Downloads ubuntu ISO file
// This would be for testing and
// Benchmark reasons
func DownloadUbuntuISO() error {
	// Download Test ubuntu ISO file
	err := downloadFile("TestingFiles/ubuntu.iso", "https://releases.ubuntu.com/20.04.3/ubuntu-20.04.3-desktop-amd64.iso?_ga=2.16094259.481697702.1636914790-159169894.1636914790")
	if err != nil {
		return err
	}
	return nil
}

// Downloads a sample PDF file
func DownloadPDFFile() error {
	err := downloadFile("TestingFiles/test.pdf", "https://resources.linuxfoundation.org/LF+Projects/CNCF/TheNewStack_Book2_KubernetesDeploymentAndSecurityPatterns.pdf")
	if err != nil {
		return err
	}
	return nil
}


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

// md5 check sum
func md5sum(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// TestSplit ensures the split function is happening as required
func TestSplit(t *testing.T) {
	// Splitting Test file with each of 100 kb
	output, err := Split("TestingFiles/test.pdf", 0.02,"TestingFiles/output/")
	if err != nil {
		fmt.Println(err)
		t.Fail()
	}

	PrettyPrint(output)
}

// Run TestSplit test before this no ensure the lime.epub-hashes file is generated
// TestReadHashes displays the output of the ReadHashes function
func TestReadHashes(t *testing.T) {
	hashes, err := ReadHashes("TestingFiles/output/test.pdf-hashes.txt")
	if err != nil {
		fmt.Println(err)
		t.Fail()
	}
	PrettyPrint(hashes)
}

// TestFileChunks_Join Joining all chunks into a single file
func TestFileChunks_Join(t *testing.T) {
	hashes, err := ReadHashes("TestingFiles/output/test.pdf-hashes.txt")
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
	RightBytes, err := GetBytesFromFile("TestingFiles/test.pdf")
	if err != nil {
		fmt.Println(err)
		t.Fail()
	}

	// get the byte value for "TestingFiles//output/lime.epub"
	CheckBytes, err := GetBytesFromFile("TestingFiles/output/test.pdf")
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

// Simple test case to download large file for test case
func TestDownloadLargeTestFile (t *testing.T) {
	err := DownloadUbuntuISO()
	if err != nil {
		fmt.Println(err)
		t.Fail()
	}
}


func TestDownloadSmallTestFile (t *testing.T) {
	err := DownloadPDFFile()
	if err != nil {
		fmt.Println(err)
		t.Fail()
	}
}

// Benchmarking splitting of files
// Ubuntu ISO file (2.8 GB)
// Split Size 20kb
func Benchmark20KbSplitLarge(b *testing.B) {
	// Splitting Test file with each of 20 kb
	_, err := Split("TestingFiles/ubuntu.iso", 0.02,"TestingFiles/output/")
	if err != nil {
		fmt.Println(err)
		b.Failed()
	}
}

// Benchmark for joining files once split
// Ubuntu ISO file (2.8 GB)
// Split Size 20kb
func BenchmarkFileChunks_20KbJoinLarge(b *testing.B) {
	hashes, err := ReadHashes("TestingFiles/output/ubuntu.iso-hashes.txt")
	if err != nil {
		fmt.Println(err)
		b.Failed()
	}

	// Joining all the chunks into a single file
	err = hashes.Join()
	if err != nil {
		fmt.Println(err)
		b.Failed()
	}
}

func Benchmark20KbSplitSmall(b *testing.B) {
	// Splitting Test file with each of 20 kb
	_, err := Split("TestingFiles/test.pdf", 0.02,"TestingFiles/output/")
	if err != nil {
		fmt.Println(err)
		b.Failed()
	}
}

func BenchmarkFileChunks_20KbJoinSmall(b *testing.B) {
	hashes, err := ReadHashes("TestingFiles/output/test.pdf-hashes.txt")
	if err != nil {
		fmt.Println(err)
		b.Failed()
	}

	// Joining all the chunks into a single file
	err = hashes.Join()
	if err != nil {
		fmt.Println(err)
		b.Failed()
	}
}
