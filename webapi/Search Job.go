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

	"github.com/PeernetOfficial/core/blockchain"
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
	SizeMin    int       // Min file size in bytes. -1 = not used.
	SizeMax    int       // Max file size in bytes. -1 = not used.
}

// SearchJob is a collection of search jobs
type SearchJob struct {
	// input settings
	id        uuid.UUID     // The job id
	timeout   time.Duration // timeout set for all searches
	maxResult int           // max results user-facing.

	filtersStart   SearchFilter // Filters when starting the search. They cannot be changed later on. Any incoming file is checked against them, even if there are different runtime filters.
	filtersRuntime SearchFilter // Runtime Filters. They allow filtering results after they were received.

	// File statistics (filters are ignored) of returned results. Map value is always count of files.
	stats struct {
		sync.RWMutex                   // Synced access to maps
		date         map[time.Time]int // Files per day (rounded down to midnight)
		fileType     map[uint8]int     // Files per File Type
		fileFormat   map[uint16]int    // Files per File Format
		total        int               // Total count of files
	}

	// -- result data --

	// Status indicates the overall search status. This will be removed later when relying on search clients.
	Status int

	// runtime data
	//clients        []*SearchClient // all search clients
	clientsMutex sync.Mutex // mutex for manipulating client list

	// List of files found but not yet returned via API to the caller. They are subject to sorting.
	Files       []*apiFile
	requireSort bool // if Files requires sort before returning the results

	// FreezeFiles is a list of files that were already finally delivered via the API. They may NOT change in sorting.
	FreezeFiles []*apiFile

	// List of all files. Does not change based on sorting or runtime filters. This list only gets expanded.
	AllFiles []*apiFile

	ResultSync sync.Mutex // ResultSync ensures unique access to the file results

	currentOffset int // for always getting the next results
}

const (
	SearchStatusNotStarted = iota // Search was not yet started
	SearchStatusLive              // Search running
	SearchStatusTerminated        // Search is terminated. No more results are expected.
	SearchStatusNoIndex           // Search is terminated. No search index to use.
)

// CreateSearchJob creates a new search job and adds it to the lookup list.
// Timeout and MaxResults must be set and must not be 0.
func (api *WebapiInstance) CreateSearchJob(Timeout time.Duration, MaxResults int, Filter SearchFilter) (job *SearchJob) {
	job = &SearchJob{}
	job.Status = SearchStatusNotStarted
	job.id = uuid.New()
	job.timeout = Timeout
	job.maxResult = MaxResults
	job.filtersStart = Filter
	job.filtersRuntime = Filter // initialize the runtime filters as the same

	job.stats.date = make(map[time.Time]int)
	job.stats.fileType = make(map[uint8]int)
	job.stats.fileFormat = make(map[uint16]int)

	// add to the list of jobs
	api.allJobsMutex.Lock()
	api.allJobs[job.id] = job
	api.allJobsMutex.Unlock()

	return
}

// ReturnResult returns the selected results.
func (job *SearchJob) ReturnResult(Offset, Limit int) (Result []*apiFile) {
	if Limit == 0 {
		return Result
	}

	job.ResultSync.Lock()
	defer job.ResultSync.Unlock()

	// serve files from frozen list?
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

		job.Files = SortFiles(job.Files, job.filtersRuntime.Sort)
	}

	// set the amount of files to copy
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

// PeekResult returns the selected results but will not change any frozen files or impact auto offset
func (job *SearchJob) PeekResult(Offset, Limit int) (Result []*apiFile) {
	job.ResultSync.Lock()
	defer job.ResultSync.Unlock()

	// serve files from frozen list?
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

		job.Files = SortFiles(job.Files, job.filtersRuntime.Sort)
	}

	countCopy := len(job.Files) - Offset
	if countCopy > Limit {
		countCopy = Limit
	}

	// copy the results
	Result = append(Result, job.Files[Offset:Offset+countCopy]...)

	return Result
}

// RuntimeFilter allows to apply filters at runtime to search jobs that already started. To remove the filters, call this function without the filters set.
func (job *SearchJob) RuntimeFilter(Filter SearchFilter) {
	job.ResultSync.Lock()
	defer job.ResultSync.Unlock()

	job.filtersRuntime = Filter

	// FreezeFiles and current offset is reset
	job.FreezeFiles = nil
	job.currentOffset = 0

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
			job.Files = SortFiles(job.Files, job.filtersRuntime.Sort)
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

	if job.filtersRuntime.SizeMin >= 0 && file.Size < uint64(job.filtersRuntime.SizeMin) || job.filtersRuntime.SizeMax >= 0 && file.Size > uint64(job.filtersRuntime.SizeMax) {
		return false
	}

	return true
}

// SortFiles sorts a list of files. It returns a sorted list. 0 = no sorting, 1 = Relevance ASC, 2 = Relevance DESC, 3 = Date ASC, 4 = Date DESC, 5 = Name ASC, 6 = Name DESC
func SortFiles(files []*apiFile, Sort int) (sorted []*apiFile) {
	switch Sort {
	case SortRelevanceAsc:
		sort.SliceStable(files, func(i, j int) bool { return files[i].Date.Before(files[j].Date) }) // first as date for secondary sorting
		//sort.SliceStable(files, func(i, j int) bool { return files[i].Score < files[j].Score }) // TODO
	case SortRelevanceDec:
		sort.SliceStable(files, func(i, j int) bool { return files[j].Date.Before(files[i].Date) }) // first as date for secondary sorting
		//sort.SliceStable(files, func(i, j int) bool { return files[i].Score > files[j].Score }) // TODO

	case SortDateAsc:
		sort.SliceStable(files, func(i, j int) bool { return files[i].Date.Before(files[j].Date) })
	case SortDateDesc:
		sort.SliceStable(files, func(i, j int) bool { return files[j].Date.Before(files[i].Date) })

	case SortNameAsc:
		sort.SliceStable(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	case SortNameDesc:
		sort.SliceStable(files, func(i, j int) bool { return files[i].Name > files[j].Name })

	case SortSizeAsc:
		sort.SliceStable(files, func(i, j int) bool { return files[i].Size < files[j].Size })
	case SortSizeDesc:
		sort.SliceStable(files, func(i, j int) bool { return files[i].Size > files[j].Size })

	case SortSharedByCountAsc:
		sort.SliceStable(files, func(i, j int) bool {
			return files[i].GetMetadata(blockchain.TagSharedByCount).Number < files[j].GetMetadata(blockchain.TagSharedByCount).Number
		})
	case SortSharedByCountDesc:
		sort.SliceStable(files, func(i, j int) bool {
			return files[i].GetMetadata(blockchain.TagSharedByCount).Number > files[j].GetMetadata(blockchain.TagSharedByCount).Number
		})

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

// RemoveJob removes the job structure from the list. Terminate should be called before. Unless the search is manually removed, it stays forever in the list.
func (api *WebapiInstance) RemoveJob(job *SearchJob) {
	api.allJobsMutex.Lock()
	delete(api.allJobs, job.id) // delete is safe to call multiple times, so auto-removal and manual one are fine and need no syncing
	api.allJobsMutex.Unlock()
}

// RemoveDefer removes the search job after a given time after all searches are terminated. This can be used for automated time delayed removal. Do not create additional search clients after deferal removing.
func (api *WebapiInstance) RemoveJobDefer(job *SearchJob, Duration time.Duration) {
	go func() {
		// for _, client := range job.clients {
		// 	<-client.TerminateSignal
		// }

		<-time.After(Duration)
		api.RemoveJob(job)
	}()
}

// JobLookup looks up a job. Returns nil if not found.
func (api *WebapiInstance) JobLookup(id uuid.UUID) (job *SearchJob) {
	api.allJobsMutex.RLock()
	job = api.allJobs[id]
	api.allJobsMutex.RUnlock()

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

	return job.Status == SearchStatusTerminated || job.Status == SearchStatusNoIndex
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

// SearchStatisticRecordDay is a single record containing date info.
type SearchStatisticRecordDay struct {
	Date  time.Time `json:"date"`  // The day (which covers the full 24 hours). Always rounded down to midnight.
	Count int       `json:"count"` // Count of files.
}

// SearchStatisticRecord is a single record.
type SearchStatisticRecord struct {
	Key   int `json:"key"`   // Key index. The exact meaning depends on where this structure is used.
	Count int `json:"count"` // Count of files for the given key
}

// SearchStatisticData contains statistics on search results.
type SearchStatisticData struct {
	Date       []SearchStatisticRecordDay `json:"date"`       // Files per date
	FileType   []SearchStatisticRecord    `json:"filetype"`   // Files per file type
	FileFormat []SearchStatisticRecord    `json:"fileformat"` // Files per file format
	Total      int                        `json:"total"`      // Total count of files
}

// Statistics generates statistics on all results, regardless of runtime filters.
func (job *SearchJob) Statistics() (result SearchStatisticData) {
	job.stats.RLock()
	defer job.stats.RUnlock()

	result.Total = job.stats.total

	// Files per date. Sort dates to date ASC.
	for key, value := range job.stats.date {
		result.Date = append(result.Date, SearchStatisticRecordDay{Date: key, Count: value})
	}
	sort.SliceStable(result.Date, func(i, j int) bool { return result.Date[i].Date.Before(result.Date[j].Date) })

	// File Type and Format
	for key, value := range job.stats.fileType {
		result.FileType = append(result.FileType, SearchStatisticRecord{Key: int(key), Count: value})
	}
	for key, value := range job.stats.fileFormat {
		result.FileFormat = append(result.FileFormat, SearchStatisticRecord{Key: int(key), Count: value})
	}

	return
}

// statsAdd counts the files in the statistics
func (job *SearchJob) statsAdd(files ...*apiFile) {
	job.stats.Lock()
	defer job.stats.Unlock()

	for _, file := range files {
		// Use file's Date field if available.
		if !file.Date.IsZero() {
			// Files per day
			date := file.Date.Truncate(24 * time.Hour)
			countDate := job.stats.date[date]
			countDate++
			job.stats.date[date] = countDate
		}

		// File Type and Format
		countType := job.stats.fileType[file.Type]
		countType++
		job.stats.fileType[file.Type] = countType

		countFormat := job.stats.fileFormat[file.Format]
		countFormat++
		job.stats.fileFormat[file.Format] = countFormat
	}

	job.stats.total += len(files)
}

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
