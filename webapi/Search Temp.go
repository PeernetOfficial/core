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
	"sync"
	"time"

	"github.com/PeernetOfficial/core"
	"github.com/google/uuid"
)

func dispatchSearch(input SearchRequest) (job *SearchJob) {
	job = &SearchJob{id: uuid.New(), timeout: time.Duration(input.Timeout) * time.Second, maxResult: input.MaxResults, sortOrder: input.Sort, fileType: -1, fileFormat: -1}

	allJobsMutex.Lock()
	allJobs[job.id] = job
	allJobsMutex.Unlock()

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
			newFile := createTestResult(-1)
			job.filesCurrent = append(job.filesCurrent, &newFile)
		}

		job.ResultSync.Unlock()

	}(job)

	job.RemoveDefer(job.timeout + time.Second*20)

	return job
}

// ---- job list management ----

// SearchJob is a collection of search jobs
type SearchJob struct {
	// input settings
	id        uuid.UUID     // The job id
	timeout   time.Duration // timeout set for all searches
	maxResult int           // max results user-facing.
	sortOrder int           // 0 = No sorting, 1 = Relevance ASC, 2 = Relevance DESC, 3 = Date ASC, 4 = Date DESC

	// additional optional filters
	isDates    bool      // whether the from/to dates are valid, both are required.
	dateFrom   time.Time // optional date from
	dateTo     time.Time // optional date to
	fileType   int       // File type such as binary, text document etc. See core.TypeX. -1 not used.
	fileFormat int       // File format such as PDF, Word, Ebook, etc. See core.FormatX. -1 not used.

	// runtime data
	//clients        []*SearchClient // all search clients
	//clientsMutex sync.Mutex // mutex for manipulating client list

	filesCurrent []*core.BlockRecordFile // Current result list of files, not yet fetched by caller.
	FilesAll     []*core.BlockRecordFile // List of all files. This list only gets expanded.
	ResultSync   sync.Mutex              // ResultSync ensures unique access to the file results
}

// job list management
var (
	allJobs      map[uuid.UUID]*SearchJob = make(map[uuid.UUID]*SearchJob)
	allJobsMutex sync.RWMutex
)

// Remove removes the structure from the list. Terminate should be called before. Unless the search is manually removed, it stays forever in the list.
func (job *SearchJob) Remove() {
	allJobsMutex.Lock()
	delete(allJobs, job.id) // delete is safe to call multiple times, so auto-removal and manual one are fine and need no syncing
	allJobsMutex.Unlock()
}

// RemoveDefer removes the search job after a given time after all searches are terminated. This can be used for automated time delayed removal. Do not create additional search clients after deferal removing.
func (job *SearchJob) RemoveDefer(Duration time.Duration) {
	go func() {
		// for _, client := range job.clients {
		// 	<-client.TerminateSignal
		// }

		<-time.After(Duration)
		job.Remove()
	}()
}

// JobLookup looks up a job. Returns nil if not found.
func JobLookup(id uuid.UUID) (job *SearchJob) {
	allJobsMutex.RLock()
	job = allJobs[id]
	allJobsMutex.RUnlock()

	return job
}

// Terminate terminates all searches
func (job *SearchJob) Terminate() {
	//job.clientsMutex.Lock()
	//defer job.clientsMutex.Unlock()

	// for n := range job.clients {
	// 	if !job.clients[n].IsTerminated {
	// 		job.clients[n].Terminate(true)
	// 	}
	// }
}

// ReturnResult returns the selected results.
func (job *SearchJob) ReturnResult(Limit int) (Result []*core.BlockRecordFile) {
	if Limit == 0 {
		return Result
	}

	job.ResultSync.Lock()
	defer job.ResultSync.Unlock()

	if len(job.filesCurrent) == 0 {
		return Result
	} else if Limit > len(job.filesCurrent) {
		Limit = len(job.filesCurrent)
	}

	Result = job.filesCurrent[:Limit]
	job.filesCurrent = job.filesCurrent[Limit:]

	return Result
}

// ReturnNext returns the next results. Call must be serialized.
func (job *SearchJob) ReturnNext(Limit int) (Result []*core.BlockRecordFile) {
	return job.ReturnResult(Limit)
}

// createTestResult creates a test file. fileType = -1 for any.
func createTestResult(fileType int) (file core.BlockRecordFile) {
	randomData := make([]byte, 10)
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
