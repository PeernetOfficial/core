package swarm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/PeernetOfficial/core/protocol"
	"io"
	"io/ioutil"
	"log"
	"math"
	"os"
	"strconv"
)

// FileChunks Struct to store information of file chunks
type fileChunks struct {
	Name          string      `json:"name"`
	FileChunk     []*fileChunk `json:"fileChunk"`
	SplitSize     float64     `json:"splitSize"`
	WriteLocation string      `json:"writeLocation"`
	CompleteFileHash string   `json:"completeFileHash"`
}

// FileChunk Struct to store information of each chunk
type fileChunk struct {
	ChunkName  string   `json:"ChunkName"`
	Hash       string   `json:"Hash"`
}

// Split method splits the files into part files of user defined lengths
func Split(filename string, splitsize float64, writelocation string)  (*fileChunks, error) {
	// Setting up the struct of type FileChunks with parameters provided
	var FileChunks fileChunks
	FileChunks.SplitSize = splitsize
	FileChunks.WriteLocation = writelocation
	// -------------------------------------------------------------------------

	bufferSize := 1024 // 1 KB for optimal splitting
	fileStats, _ := os.Stat(filename)
	pieces := int(math.Ceil(float64(fileStats.Size()) / float64(splitsize*1048576)))
	nTimes := int(float64(math.Ceil(splitsize*1048576)) / float64(bufferSize))
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	// Name of the file
	fileName := fileStats.Name()
	FileChunks.Name = fileName

	i := 1
	for i <= pieces {
		// Appending each element to the struct of type FileChunk
		var FileChunk fileChunk
		FileChunk.ChunkName = fileName + ".pt" + strconv.Itoa(i)

		partFileName := fileName + ".pt" + strconv.Itoa(i)
		pfile, err := os.OpenFile(writelocation + partFileName, os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, err
		}

		buffer := make([]byte, bufferSize)
		j := 1
		//if i <= pieces - 1 {
		//	for j <= nTimes - 2 {
		//		_, inFileErr := file.Read(buffer)
		//		if inFileErr == io.EOF {
		//			break
		//		}
		//		_, err2 := pfile.Write(buffer)
		//		if err2 != nil {
		//			return nil, err2
		//		}
		//		j++
		//	}
		//} else {
			for j <= nTimes {
				_, inFileErr := file.Read(buffer)
				if inFileErr == io.EOF {
					break
				}
				_, err2 := pfile.Write(buffer)
				if err2 != nil {
					return nil, err2
				}
				j++
		//	}

		}


		partFileHash :=  protocol.HashDataString([]byte(partFileName))

		FileChunk.Hash = partFileHash
		FileChunks.FileChunk = append(FileChunks.FileChunk, &FileChunk)

		pfile.Close()
		i++
	}

	fileNameHash :=  protocol.HashDataString([]byte(filename))
	FileChunks.CompleteFileHash = fileNameHash

	file.Close()

	// write information to the main file
	err = FileChunks.writeFile()
	if err != nil {
		return nil, err
	}

	return &FileChunks, nil
}

// Join method joins the split files into one, original file
func (chunks *fileChunks)Join() error {
	a := len(chunks.FileChunk[0].ChunkName)
	b := a - 4
	iFileName := chunks.WriteLocation + chunks.FileChunk[0].ChunkName[:b]
	_, err := os.Create(iFileName)
	jointFile, err := os.OpenFile(iFileName, os.O_APPEND|os.O_WRONLY, os.ModeAppend)
	if err != nil {
		return err
	}
	i := 1
	for i <= len(chunks.FileChunk) {
		partFileName := iFileName + ".pt" + strconv.Itoa(i)

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

	return nil
}

// write information of all chunks available
func (chunks *fileChunks)writeFile() error {
	file, err := json.MarshalIndent(chunks, "", " ")
	if err != nil {
		return err
	}

	err = os.WriteFile(chunks.WriteLocation + chunks.Name + "-hashes.txt", file, 0644)
	if err != nil {
		return err
	}

	return nil
}

// ReadHashes Reads -hashes.txt file
// and adds the information to the struct
// - hashesFile: path of the -hashesFile
func ReadHashes(hashesFile string) (*fileChunks, error) {
	jsonFile, err := os.Open(hashesFile)
	// if we os.Open returns an error then handle it
	if err != nil {
		return nil,err
	}


	// defer the closing of our jsonFile so that we can parse it later on
	defer jsonFile.Close()

	byteValue, err := ioutil.ReadAll(jsonFile)
	if err != nil {
		return nil, err
	}

	// we initialize our Users array
	var FileChunks *fileChunks

	// we unmarshal our byteArray which contains our
	// jsonFile's content into 'users' which we defined above
	json.Unmarshal(byteValue, &FileChunks)

	return FileChunks, nil
}

// PrettyPrint Implementing a pretty print function to print struct output
func PrettyPrint(data interface{}){
	var p []byte
	//    var err := error
	p, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		fmt.Println(err)
	}
	fmt.Printf("%s \n", p)
}