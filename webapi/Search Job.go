/*
File Name:  Search Job.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SearchFilter allows to filter search results based on the criteria.
type SearchFilter struct {
	IsDates    bool      // Whether the from/to dates are valid, both are required.
	DateFrom   time.Time // Optional date from
	DateTo     time.Time // Optional date to
	FileType   int       // File type such as binary, text document etc. See core.TypeX. -1 = not used.
	FileFormat int       // File format such as PDF, Word, Ebook, etc. See core.FormatX. -1 = not used.
	Sort       int       // Sort order. See SortX.
}

// SearchJob is a collection of search jobs
type SearchJob struct {
	// input settings
	id        uuid.UUID     // The job id
	timeout   time.Duration // timeout set for all searches
	maxResult int           // max results user-facing.

	filtersStart   SearchFilter // Filters when starting the search. They cannot be changed later on. Any incoming file is checked against them, even if there are different runtime filters.
	filtersRuntime SearchFilter // Runtime Filters. They allow filtering results after they were received.

	// -- result data --

	// runtime data
	//clients        []*SearchClient // all search clients
	clientsMutex sync.Mutex // mutex for manipulating client list

	// List of files found but not yet returned via API to the caller. They are subject to sorting.
	Files       []*apiFile
	requireSort bool // if Files requires sort before returning the results

	// FreezeFiles is a list of items that were already finally delivered via the API. They may NOT change in sorting.
	FreezeFiles []*apiFile

	// List of all files. Does not change based on sorting or runtime filters. This list only gets expanded.
	AllFiles []*apiFile

	ResultSync sync.Mutex // ResultSync ensures unique access to the file results

	currentOffset int // for always getting the next results
}

// CreateSearchJob creates a new search job and adds it to the lookup list.
// Timeout and MaxResults must be set and must not be 0.
func CreateSearchJob(Timeout time.Duration, MaxResults, Sort, FileType, FileFormat int, DateFrom, DateTo time.Time) (job *SearchJob) {
	job = &SearchJob{}
	job.id = uuid.New()
	job.timeout = Timeout
	job.maxResult = MaxResults

	job.filtersStart = SearchFilter{Sort: Sort, FileType: FileType, FileFormat: FileFormat}

	if !DateFrom.IsZero() && !DateTo.IsZero() && DateFrom.Before(DateTo) {
		job.filtersStart.DateFrom = DateFrom
		job.filtersStart.DateTo = DateTo
		job.filtersStart.IsDates = true
	}

	// initialize the runtime filters as the same
	job.filtersRuntime = job.filtersStart

	// add to the list of jobs
	allJobsMutex.Lock()
	allJobs[job.id] = job
	allJobsMutex.Unlock()

	return
}

// ReturnResult returns the selected results.
func (job *SearchJob) ReturnResult(Offset, Limit int) (Result []*apiFile) {
	if Limit == 0 {
		return Result
	}

	job.ResultSync.Lock()
	defer job.ResultSync.Unlock()

	// serve files from freezed list?
	if Offset < len(job.FreezeFiles) {
		countCopy := len(job.FreezeFiles) - Offset
		if countCopy > Limit {
			countCopy = Limit
		}
		Result = job.FreezeFiles[Offset : Offset+countCopy]
		Limit -= countCopy
		Offset = 0
	} else {
		Offset -= len(job.FreezeFiles)
	}

	if Limit == 0 {
		return Result
	}

	// go through the live results and fill the list
	if Offset >= len(job.Files) { // offset wants to skip entire queue?
		job.FreezeFiles = append(job.FreezeFiles, job.Files...)
		job.Files = nil
		return Result
	}

	// check if a sort is required before using this queue
	if job.requireSort {
		job.requireSort = false

		job.Files = SortItems(job.Files, job.filtersRuntime.Sort)
	}

	// set the amount of items to copy
	countCopy := len(job.Files) - Offset
	if countCopy > Limit {
		countCopy = Limit
	}

	// copy the results and freeze them
	Result = append(Result, job.Files[Offset:Offset+countCopy]...)

	// note that freeze disregards the offset, it has to freeze any elements before!
	job.FreezeFiles = append(job.FreezeFiles, job.Files[:Offset+countCopy]...)
	job.Files = job.Files[Offset+countCopy:]

	//Limit -= countCopy

	return Result
}

// ReturnNext returns the next results. Call must be serialized.
func (job *SearchJob) ReturnNext(Limit int) (Result []*apiFile) {
	Result = job.ReturnResult(job.currentOffset, Limit)
	job.currentOffset += len(Result)

	return
}

// PeekResult returns the selected results but will not change any freezed items or impact auto offset
func (job *SearchJob) PeekResult(Offset, Limit int) (Result []*apiFile) {
	job.ResultSync.Lock()
	defer job.ResultSync.Unlock()

	// serve items from freezed list?
	if Offset < len(job.FreezeFiles) {
		countCopy := len(job.FreezeFiles) - Offset
		if countCopy > Limit {
			countCopy = Limit
		}
		Result = job.FreezeFiles[Offset : Offset+countCopy]
		Limit -= countCopy
		Offset = 0
	} else {
		Offset -= len(job.FreezeFiles)
	}

	if Limit == 0 || Offset >= len(job.Files) { // offset wants to skip entire queue?
		return Result
	}

	// check if a sort is required before using this queue
	if job.requireSort {
		job.requireSort = false

		job.Files = SortItems(job.Files, job.filtersRuntime.Sort)
	}

	countCopy := len(job.Files) - Offset
	if countCopy > Limit {
		countCopy = Limit
	}

	// copy the results
	Result = append(Result, job.Files[Offset:Offset+countCopy]...)

	return Result
}

// RuntimeFilter allows to apply filters at runtime to search jobs that already started.
// To remove the filters, call this function without the filters set. Sort 0 = none, file type and format -1 = not used, dates 0 = not used.
func (job *SearchJob) RuntimeFilter(Sort, FileType, FileFormat int, DateFrom, DateTo time.Time) {
	job.ResultSync.Lock()
	defer job.ResultSync.Unlock()

	// FreezeFiles and current offset is reset
	job.FreezeFiles = nil
	job.currentOffset = 0

	job.filtersRuntime.Sort = Sort
	job.filtersRuntime.FileType = FileType
	job.filtersRuntime.FileFormat = FileFormat

	if !DateFrom.IsZero() && !DateTo.IsZero() {
		job.filtersRuntime.DateFrom = DateFrom
		job.filtersRuntime.DateTo = DateTo
		job.filtersRuntime.IsDates = true
	} else {
		job.filtersRuntime.DateFrom = time.Time{}
		job.filtersRuntime.DateTo = time.Time{}
		job.filtersRuntime.IsDates = false
	}

	// files remain in AllFiles, but Files needs to be filtered based on the new filter
	job.Files = []*apiFile{}
	job.requireSort = false // Sorting is done immediately below

	// set Files based on AllFiles with the filter
	for m := range job.AllFiles {
		// only append if filter is matching
		if job.isFileFiltered(job.AllFiles[m]) {
			job.Files = append(job.Files, job.AllFiles[m])
		}

		// sort, if a sort order is defined
		if job.filtersRuntime.Sort > 0 {
			job.Files = SortItems(job.Files, job.filtersRuntime.Sort)
		}
	}
}

// isFileFiltered returns true if the file conforms to the runtime filter. If there is no runtime filter, it always returns true.
func (job *SearchJob) isFileFiltered(file *apiFile) bool {
	if job.filtersRuntime.FileType >= 0 && file.Type != uint8(job.filtersRuntime.FileType) {
		return false
	}

	if job.filtersRuntime.FileFormat >= 0 && file.Format != uint16(job.filtersRuntime.FileFormat) {
		return false
	}

	// Note: If the date is not available in the file, it will be filtered out. Since this is the mapped Shared Date this should normally not occur though.
	if job.filtersRuntime.IsDates && (file.Date.IsZero() || file.Date.Before(job.filtersRuntime.DateFrom) || file.Date.After(job.filtersRuntime.DateTo)) {
		return false
	}

	return true
}

// SortItems sorts a list of files. It returns a sorted list. 0 = no sorting, 1 = Relevance ASC, 2 = Relevance DESC, 3 = Date ASC, 4 = Date DESC, 5 = Name ASC, 6 = Name DESC
func SortItems(files []*apiFile, Sort int) (sorted []*apiFile) {
	if Sort == 0 {
		return files
	}

	// sort!
	switch Sort {
	case 1: // Relevance Score ASC
		sort.SliceStable(files, func(i, j int) bool { return files[i].Date.Before(files[j].Date) }) // first as date for secondary sorting
		//sort.SliceStable(files, func(i, j int) bool { return files[i].Score < files[j].Score }) // TODO
	case 2: // Relevance Score DESC
		sort.SliceStable(files, func(i, j int) bool { return files[j].Date.Before(files[i].Date) }) // first as date for secondary sorting
		//sort.SliceStable(files, func(i, j int) bool { return files[i].Score > files[j].Score }) // TODO
	case 3: // Date ASC
		sort.SliceStable(files, func(i, j int) bool { return files[i].Date.Before(files[j].Date) })
	case 4: // Date DESC
		sort.SliceStable(files, func(i, j int) bool { return files[j].Date.Before(files[i].Date) })
	}

	return files
}

// IsSearchResults checks if search results may be expected (either files are in queue or a search is running)
func (job *SearchJob) IsSearchResults() bool {
	// check for any available results. Do not use any lock here as this is read only.
	return len(job.Files) > 0 || !job.IsTerminated()
}

// isFileReceived checks if a file was already received, preventing double results
func (job *SearchJob) isFileReceived(id uuid.UUID) (exists bool) {
	// Future: A map would be likely faster than iterating over all results.
	for m := range job.AllFiles {
		if id == job.AllFiles[m].ID {
			return true
		}
	}

	return false
}

// ---- job list management ----
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

// IsTerminated checks if all searches are finished. The job itself does not record a termination signal.
func (job *SearchJob) IsTerminated() bool {
	job.clientsMutex.Lock()
	defer job.clientsMutex.Unlock()

	// for n := range job.clients {
	// 	if !job.clients[n].IsTerminated {
	// 		return false
	// 	}
	// }

	return false
}

// Terminate terminates all searches
func (job *SearchJob) Terminate() {
	job.clientsMutex.Lock()
	defer job.clientsMutex.Unlock()

	// for n := range job.clients {
	// 	if !job.clients[n].IsTerminated {
	// 		job.clients[n].Terminate(true)
	// 	}
	// }
}

// WaitTerminate waits until all search clients are terminated. Do not create additional search clients after calling this function.
func (job *SearchJob) WaitTerminate() {
	//for _, client := range job.clients {
	//	<-client.TerminateSignal
	//}
}

// ---- statistics ----

// ---- actual search & retrieving results ----

// appendSearchClient appends a search client to a job

func (job *SearchJob) SearchAway() {
	job.clientsMutex.Lock()
	defer job.clientsMutex.Unlock()

	// TODO
}

//func (job *SearchJob) appendSearchClient(search *dht.SearchClient) {
//}

//func (job *SearchJob) receiveClientResults(client *dht.SearchClient) {
//}
