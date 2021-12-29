/*
File Name:  Search.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner

/search                 Submit a search request
/search/result          Return search results
/search/terminate       Terminate a search
/search/result/ws       Websocket to receive results as stream
/search/statistic       Statistics about the results

/explore                List recently shared files

*/

package webapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// SearchRequest is the information from the end-user for the search. Filters and sort order may be applied when starting the search, or at runtime when getting the results.
type SearchRequest struct {
	Term        string      `json:"term"`       // Search term.
	Timeout     int         `json:"timeout"`    // Timeout in seconds. 0 means default. This is the entire time the search may take. Found results are still available after this timeout.
	MaxResults  int         `json:"maxresults"` // Total number of max results. 0 means default.
	DateFrom    string      `json:"datefrom"`   // Date from, both from/to are required if set. Format "2006-01-02 15:04:05".
	DateTo      string      `json:"dateto"`     // Date to, both from/to are required if set. Format "2006-01-02 15:04:05".
	Sort        int         `json:"sort"`       // See SortX.
	TerminateID []uuid.UUID `json:"terminate"`  // Optional: Previous search IDs to terminate. This is if the user makes a new search from the same tab. Same as first calling /search/terminate.
	FileType    int         `json:"filetype"`   // File type such as binary, text document etc. See core.TypeX. -1 = not used.
	FileFormat  int         `json:"fileformat"` // File format such as PDF, Word, Ebook, etc. See core.FormatX. -1 = not used.
	SizeMin     int         `json:"sizemin"`    // Min file size in bytes. -1 = not used.
	SizeMax     int         `json:"sizemax"`    // Max file size in bytes. -1 = not used.
}

// Sort orders
const (
	SortNone              = 0  // No sorting. Results are returned as they come in.
	SortRelevanceAsc      = 1  // Least relevant results first.
	SortRelevanceDec      = 2  // Most relevant results first.
	SortDateAsc           = 3  // Oldest first.
	SortDateDesc          = 4  // Newest first.
	SortNameAsc           = 5  // File name ascending. The folder name is not used for sorting.
	SortNameDesc          = 6  // File name descending. The folder name is not used for sorting.
	SortSizeAsc           = 7  // File size ascending. Smallest files first.
	SortSizeDesc          = 8  // File size descending. Largest files first.
	SortSharedByCountAsc  = 9  // Shared by count ascending. Files that are shared by the least count of peers first.
	SortSharedByCountDesc = 10 // Shared by count descending. Files that are shared by the most count of peers first.
)

// SearchRequestResponse is the result to the initial search request
type SearchRequestResponse struct {
	ID     uuid.UUID `json:"id"`     // ID of the search job. This is used to get the results.
	Status int       `json:"status"` // Status of the search: 0 = Success (ID valid), 1 = Invalid Term, 2 = Error Max Concurrent Searches
}

// SearchResult contains the search results.
type SearchResult struct {
	Status    int         `json:"status"`    // Status: 0 = Success with results, 1 = No more results available, 2 = Search ID not found, 3 = No results yet available keep trying
	Files     []apiFile   `json:"files"`     // List of files found
	Statistic interface{} `json:"statistic"` // Statistics of all results (independent from applied filters), if requested. Only set if files are returned (= if statistics changed). See SearchStatisticData.
}

// SearchStatistic contains statistics on search results. Statistics are always calculated over all results, regardless of any applied runtime filters.
type SearchStatistic struct {
	SearchStatisticData
	Status       int  `json:"status"`     // Status: 0 = Success
	IsTerminated bool `json:"terminated"` // Whether the search is terminated, meaning that statistics won't change
}

const apiDateFormat = "2006-01-02 15:04:05"

/*
apiSearch submits a search request

Request:    POST /search with JSON SearchRequest
Result:     200 on success with JSON SearchRequestResponse
            400 on invalid JSON
*/
func (api *WebapiInstance) apiSearch(w http.ResponseWriter, r *http.Request) {

	var input SearchRequest
	if err := DecodeJSON(w, r, &input); err != nil {
		return
	}

	if input.Timeout <= 0 {
		input.Timeout = 20
	}
	if input.MaxResults <= 0 {
		input.MaxResults = 200
	}

	// Terminate previous searches, if their IDs were supplied. This allows terminating the old search immediately without making a separate /search/terminate request.
	for _, terminate := range input.TerminateID {
		if job := api.JobLookup(terminate); job != nil {
			job.Terminate()
			api.RemoveJob(job)
		}
	}

	job := api.dispatchSearch(input)

	EncodeJSON(api.backend, w, r, SearchRequestResponse{Status: 0, ID: job.id})
}

/*
apiSearchResult returns results. The default limit is 100.
If reset is set, all results will be filtered and sorted according to the settings. This means that the new first result will be returned again and internal result offset is set to 0.

Request:    GET /search/result?id=[UUID]&limit=[max records]
Optional parameters:
			&reset=[0|1] to reset the filters or sort orders with any of the below parameters (all required):
			&filetype=[File Type]
			&fileformat=[File Format]
			&from=[Date From]&to=[Date To]
			&sizemin=[Minimum file size]
			&sizemax=[Maximum file size]
			&sort=[sort order]
Result:     200 with JSON structure SearchResult. Check the field status.
*/
func (api *WebapiInstance) apiSearchResult(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	jobID, err := uuid.Parse(r.Form.Get("id"))
	if err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	limit, err := strconv.Atoi(r.Form.Get("limit"))
	if err != nil {
		limit = 100
	}

	// find the job ID
	job := api.JobLookup(jobID)
	if job == nil {
		EncodeJSON(api.backend, w, r, SearchResult{Status: 2})
		return
	}

	// filters and sort parameter
	if filterReset, _ := strconv.ParseBool(r.Form.Get("reset")); filterReset {
		fileType, _ := strconv.Atoi(r.Form.Get("filetype"))
		fileFormat, _ := strconv.Atoi(r.Form.Get("fileformat"))
		dateFrom := r.Form.Get("from")
		dateTo := r.Form.Get("to")
		sort, _ := strconv.Atoi(r.Form.Get("sort"))
		sizeMin, _ := strconv.Atoi(r.Form.Get("sizemin"))
		sizeMax, _ := strconv.Atoi(r.Form.Get("sizemax"))

		filter := inputToSearchFilter(sort, fileType, fileFormat, dateFrom, dateTo, sizeMin, sizeMax)

		job.RuntimeFilter(filter)
	}

	// query all results
	resultFiles := job.ReturnNext(limit)

	var result SearchResult
	result.Files = []apiFile{}

	// loop over results
	for n := range resultFiles {
		result.Files = append(result.Files, *resultFiles[n])
	}

	// set the status
	if len(result.Files) > 0 {
		if job.IsSearchResults() {
			result.Status = 0 // 0 = Success with results
		} else {
			result.Status = 1 // No more results to expect
		}
	} else {
		switch job.Status {
		case SearchStatusLive:
			result.Status = 3 // No results yet available keep trying

		case SearchStatusTerminated:
			result.Status = 1 // No more results to expect

		default: // SearchStatusNoIndex, SearchStatusNotStarted
			result.Status = 1 // No more results to expect
		}
	}

	// embedded statistics?
	if returnStats, _ := strconv.ParseBool(r.Form.Get("stats")); returnStats {
		result.Statistic = job.Statistics()
	}

	EncodeJSON(api.backend, w, r, result)
}

/*
apiSearchResultStream provides a websocket to receive results as stream.

Request:    GET /search/result/ws?id=[UUID]&limit=[optional max records]
Result:     If successful, upgrades to a websocket and sends JSON structure SearchResult messages.
            Limit is optional. Not used if ommitted or 0.
*/
func (api *WebapiInstance) apiSearchResultStream(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	jobID, err := uuid.Parse(r.Form.Get("id"))
	if err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	limit, err := strconv.Atoi(r.Form.Get("limit"))
	useLimit := err == nil

	// look up the job
	job := api.JobLookup(jobID)
	if job == nil {
		EncodeJSON(api.backend, w, r, SearchResult{Status: 2})
		return
	}

	// upgrade to websocket
	conn, err := WSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// gorilla will automatically respond with "400 Bad Request", no other response is therefore necessary
		return
	}

	defer conn.Close()

	// loop to get new results and send out via the web socket.
	// Only exit if limit is reached if used, otherwise only if there are no result or the connection breaks.
	for {
		// query all results
		var resultFiles []*apiFile

		queryCount := 1
		if useLimit {
			queryCount = limit
		}
		resultFiles = job.ReturnNext(queryCount)

		if useLimit {
			limit -= len(resultFiles)
		}

		// loop over results
		var result SearchResult
		result.Files = []apiFile{}

		for n := range resultFiles {
			result.Files = append(result.Files, *resultFiles[n])
		}

		if !job.IsSearchResults() {
			result.Status = 1 // No more results to expect

			if len(result.Files) == 0 {
				conn.WriteJSON(result) // final message
				return
			}
		}

		// if no results, stall
		if len(result.Files) == 0 {
			time.Sleep(time.Millisecond * 100)
			continue
		}

		// send out the results via the websocket
		if err := conn.WriteJSON(result); err != nil {
			return
		}

		// Check whether to continue. If the limit is used break once all done.
		if (useLimit && limit <= 0) || result.Status == 1 {
			break
		}
	}
}

/*
apiSearchTerminate terminates a search

Request:    GET /search/terminate?id=[UUID]
Response:   204 Empty
            400 Invalid input
            404 ID not found
*/
func (api *WebapiInstance) apiSearchTerminate(w http.ResponseWriter, r *http.Request) {

	r.ParseForm()
	jobID, err := uuid.Parse(r.Form.Get("id"))
	if err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	// look up the job
	job := api.JobLookup(jobID)
	if job == nil {
		http.Error(w, "", http.StatusNotFound)
		return
	}

	// terminate and remove it from the list
	job.Terminate()
	api.RemoveJob(job)

	w.WriteHeader(http.StatusNoContent)
}

/*
apiSearchStatistic returns search result statistics. Statistics are always calculated over all results, regardless of any applied runtime filters.

Request:    GET /search/result?id=[UUID]
Result:     200 with JSON structure SearchStatistic. Check the field status (0 = Success, 2 = ID not found).
*/
func (api *WebapiInstance) apiSearchStatistic(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	jobID, err := uuid.Parse(r.Form.Get("id"))
	if err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	// find the job ID
	job := api.JobLookup(jobID)
	if job == nil {
		EncodeJSON(api.backend, w, r, SearchStatistic{Status: 2})
		return
	}

	stats := job.Statistics()

	EncodeJSON(api.backend, w, r, SearchStatistic{SearchStatisticData: stats, Status: 0, IsTerminated: job.IsTerminated()})
}

/*
apiExplore returns recently shared files in Peernet. Results are returned in real-time. The file type is an optional filter. See TypeX.
Special type -2 = Binary, Compressed, Container, Executable. This special type includes everything except Documents, Video, Audio, Ebooks, Picture, Text.

Request:    GET /explore?limit=[max records]&type=[file type]
Result:     200 with JSON structure SearchResult. Check the field status.
*/
func (api *WebapiInstance) apiExplore(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	limit, err := strconv.Atoi(r.Form.Get("limit"))
	if err != nil {
		limit = 100
	}
	fileType, err := strconv.Atoi(r.Form.Get("type"))
	if err != nil {
		fileType = -1
	}

	resultFiles := api.queryRecentShared(api.backend, fileType, uint64(limit*20/100), uint64(limit))

	var result SearchResult
	result.Files = []apiFile{}

	// loop over results
	for n := range resultFiles {
		result.Files = append(result.Files, blockRecordFileToAPI(resultFiles[n]))
	}

	if len(result.Files) == 0 {
		result.Status = 3 // No results yet available keep trying
	}

	result.Status = 1 // No more results to expect

	EncodeJSON(api.backend, w, r, result)
}

func (input *SearchRequest) Parse() (Timeout time.Duration) {
	if input.Timeout == 0 {
		Timeout = time.Second * 20 // default timeout: 20 seconds
	} else {
		Timeout = time.Duration(input.Timeout) * time.Second
	}

	return
}

// ToSearchFilter converts the user input to a valid search filter
func (input *SearchRequest) ToSearchFilter() (output SearchFilter) {
	return inputToSearchFilter(input.Sort, input.FileType, input.FileFormat, input.DateFrom, input.DateTo, input.SizeMin, input.SizeMax)
}

func inputToSearchFilter(Sort, FileType, FileFormat int, DateFrom, DateTo string, SizeMin, SizeMax int) (output SearchFilter) {
	output.Sort = Sort
	output.FileType = FileType
	output.FileFormat = FileFormat
	output.SizeMin = SizeMin
	output.SizeMax = SizeMax

	dateFrom, errFrom := time.Parse(apiDateFormat, DateFrom)
	dateTo, errTo := time.Parse(apiDateFormat, DateTo)
	if errFrom == nil && errTo == nil && dateFrom.Before(dateTo) {
		output.DateFrom = dateFrom
		output.DateTo = dateTo
		output.IsDates = true
	}

	return
}
