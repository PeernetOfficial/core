package torrent

import (
	"bufio"
	"fmt"
	"github.com/PeernetOfficial/core/protocol"
	"io"
	"log"
	"math"
	"os"
	"strconv"
)

// source: https://github.com/IamRaviTejaG/go-split-join/blob/master/sj/sj.go

// Split method splits the files into part files of user defined lengths
func Split(filename string, splitsize int)  error {
	bufferSize := 1024 // 1 KB for optimal splitting
	fileStats, _ := os.Stat(filename)
	pieces := int(math.Ceil(float64(fileStats.Size()) / float64(splitsize*1048576)))
	nTimes := int(math.Ceil(float64(splitsize*1048576) / float64(bufferSize)))
	file, err := os.Open(filename)
	hashFileName := filename + "-split-hash.txt"
	hashFile, err := os.OpenFile(hashFileName, os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	i := 1
	for i <= pieces {
		partFileName := filename + ".pt" + strconv.Itoa(i)
		pfile, _ := os.OpenFile(partFileName, os.O_CREATE|os.O_WRONLY, 0644)
		// TODO: --- To be removed ---
		fmt.Println("Creating file:", partFileName)
		// --- To be removed ---
		buffer := make([]byte, bufferSize)
		j := 1
		for j <= nTimes {
			_, inFileErr := file.Read(buffer)
			if inFileErr == io.EOF {
				break
			}
			_, err2 := pfile.Write(buffer)
			if err2 != nil {
				return err2
			}
			j++
		}
		partFileHash :=  protocol.HashData([]byte(partFileName))
		s := partFileName + ": " + string(partFileHash) + "\n"
		hashFile.WriteString(s)
		pfile.Close()
		i++
	}
	fileNameHash :=  protocol.HashData([]byte(filename))
	s := "Original file hash: " + string(fileNameHash) + "\n"
	hashFile.WriteString(s)
	file.Close()
	hashFile.Close()
	// TODO: --- To be removed ---
	fmt.Printf("Splitted successfully! Find the individual file hashes in %s", hashFileName)
	// --- To be removed ---

	return nil
}

// Join method joins the split files into one, original file
func Join(startFileName string, numberParts int) error {
	a := len(startFileName)
	b := a - 4
	iFileName := startFileName[:b]
	_, err := os.Create(iFileName)
	jointFile, err := os.OpenFile(iFileName, os.O_APPEND|os.O_WRONLY, os.ModeAppend)
	if err != nil {
		return err
	}
	i := 1
	for i <= numberParts {
		partFileName := iFileName + ".pt" + strconv.Itoa(i)
		fmt.Println("Processing file:", partFileName)
		pfile, _ := os.Open(partFileName)
		pfileinfo, err := pfile.Stat()
		if err != nil {
			log.Fatal(err)
		}
		pfilesize := pfileinfo.Size()
		pfileBytes := make([]byte, pfilesize)
		readSrc := bufio.NewReader(pfile)
		_, err = readSrc.Read(pfileBytes)
		if err != nil {
			return err
		}
		_, err = jointFile.Write(pfileBytes)
		if err != nil {
			return err
		}
		pfile.Close()
		jointFile.Sync()
		pfileBytes = nil
		i++
	}
	jointFile.Close()

	// TODO: --- To be removed ---
	fmt.Printf("Combined successfully!")
	// --- To be removed ---

	return nil
}
