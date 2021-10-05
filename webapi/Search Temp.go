/*
File Name:  Search Temp.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner

Temporary search code to provide dummy results for testing. To be replaced!
*/

package webapi

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"time"

	"github.com/PeernetOfficial/core"
	"github.com/google/uuid"
)

func dispatchSearch(input SearchRequest) (job *SearchJob) {
	Timeout, FileType, FileFormat, DateFrom, DateTo := input.Parse()

	// create the search job
	job = CreateSearchJob(Timeout, input.MaxResults, input.Sort, FileType, FileFormat, DateFrom, DateTo)

	// Create test data
	// * Between 0-100 results
	// * Delay of first result is 0-5 seconds
	// * File name, type, format, ID and hash are randomized
	go func(job *SearchJob) {
		rand.Seed(time.Now().UnixNano())
		waitTime := time.Duration(rand.Intn(5)) * time.Second
		countResults := rand.Intn(100)

		time.Sleep(waitTime)
		job.ResultSync.Lock()

		for n := 0; n < countResults; n++ {
			newFile := blockRecordFileToAPI(createTestResult(-1))

			// TODO: Move to channel!
			job.Files = append(job.Files, &newFile)
			job.AllFiles = append(job.AllFiles, &newFile)
			job.requireSort = true
		}

		job.ResultSync.Unlock()
		job.Terminate()

	}(job)

	job.RemoveDefer(job.timeout + time.Minute*10)

	return job
}

// createTestResult creates a test file. fileType = -1 for any.
func createTestResult(fileType int) (file core.BlockRecordFile) {
	randomData := make([]byte, 10*1024)
	rand.Read(randomData)

	file.Hash = core.Data2Hash(randomData)
	file.Format = uint16(rand.Intn(core.FormatCSV))
	file.ID = uuid.New()
	file.Size = uint64(len(randomData))

	file.NodeID = make([]byte, 32) // node ID = blake3 hash of peer ID
	rand.Read(file.NodeID)

	if fileType == -1 {
		switch file.Format {
		case core.FormatCSV, core.FormatEmail, core.FormatText, core.FormatHTML:
			file.Type = core.TypeText

		case core.FormatDatabase:
			file.Type = core.TypeBinary

		case core.FormatCompressed:
			file.Type = core.TypeCompressed

		case core.FormatContainer:
			file.Type = core.TypeContainer

		case core.FormatEbook:
			file.Type = core.TypeEbook

		case core.FormatVideo:
			file.Type = core.TypeVideo

		case core.FormatAudio:
			file.Type = core.TypeAudio

		case core.FormatPicture:
			file.Type = core.TypePicture

		case core.FormatPowerpoint, core.FormatExcel, core.FormatWord, core.FormatPDF:
			file.Type = core.TypeDocument

		case core.FormatFolder:
			file.Type = core.TypeFolder

		default:
			file.Type = core.TypeBinary

		}
	} else {
		if fileType == -2 {
			// Binary, Compressed, Container, Executable
			otherList := []int{core.TypeBinary, core.TypeCompressed, core.TypeContainer, core.TypeExecutable}
			fileType = otherList[rand.Intn(len(otherList))]
		}

		file.Type = uint8(fileType)
		switch file.Type {
		case core.TypeBinary:
			file.Format = core.FormatBinary

		case core.TypeText:
			file.Format = core.FormatText

		case core.TypePicture:
			file.Format = core.FormatPicture

		case core.TypeVideo:
			file.Format = core.FormatVideo

		case core.TypeAudio:
			file.Format = core.FormatAudio

		case core.TypeDocument:
			file.Format = core.FormatPDF

		case core.TypeExecutable:
			file.Format = core.FormatExecutable

		case core.TypeContainer:
			file.Format = core.FormatContainer

		case core.TypeCompressed:
			file.Format = core.FormatCompressed

		case core.TypeFolder:
			file.Format = core.FormatFolder

		case core.TypeEbook:
			file.Format = core.FormatEbook

		default:
			file.Format = core.FormatBinary
		}
	}

	var extension string
	switch file.Type {
	case core.TypeBinary:
		extension = "bin"

	case core.TypeText:
		extension = "txt"

	case core.TypePicture:
		extension = "jpg"

	case core.TypeVideo:
		extension = "mp4"

	case core.TypeAudio:
		extension = "mp3"

	case core.TypeDocument:
		extension = "pdf"

	case core.TypeExecutable:
		extension = "exe"

	case core.TypeContainer:
		extension = "zip"

	case core.TypeCompressed:
		extension = "gz"

	case core.TypeFolder:
		extension = ""

	case core.TypeEbook:
		extension = ""

	default:
		extension = ".bin"
	}

	file.Tags = append(file.Tags, core.TagFromText(core.TagName, tempFileName("", extension)))
	//file.Tags = append(file.Tags, core.TagFromText(core.TagFolder, "not set"))
	file.Tags = append(file.Tags, core.TagFromDate(core.TagDateShared, time.Now().UTC()))

	sharedByCount := uint64(rand.Intn(10))
	file.Tags = append(file.Tags, core.TagFromNumber(core.TagSharedByCount, sharedByCount))

	if sharedByCount > 0 {
		var sharedByGeoIP string
		for n := uint64(0); n < sharedByCount; n++ {
			latitude, longitude := randomGeoIP()
			if n > 0 {
				sharedByGeoIP += "\n"
			}

			sharedByGeoIP += fmt.Sprintf("%.4f", latitude) + "," + fmt.Sprintf("%.4f", longitude)
		}

		file.Tags = append(file.Tags, core.TagFromText(core.TagSharedByGeoIP, sharedByGeoIP))
	}

	return
}

// tempFileName generates a temporary filename for use in testing
func tempFileName(prefix, suffix string) string {
	randBytes := make([]byte, 16)
	rand.Read(randBytes)
	return prefix + hex.EncodeToString(randBytes) + "." + suffix
}

// randomGeoIP generates random geo IP coordinates
func randomGeoIP() (latitude, longitude float32) {
	// Latitude generates latitude (from -90.0 to 90.0)
	latitude = rand.Float32()*180 - 90

	// Longitude generates longitude (from -180 to 180)
	longitude = rand.Float32()*360 - 180

	return
}

// queryRecentShared returns recently shared files on the network. fileType = -1 for any.
func queryRecentShared(fileType, limit int) (files []*core.BlockRecordFile) {
	for n := 0; n < limit; n++ {
		newFile := createTestResult(fileType)
		files = append(files, &newFile)
	}

	return
}
