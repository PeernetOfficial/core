/*
File Name:  Search.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner

/search                 Submit a search request
/search/result          Return search results
/search/terminate       Terminate a search
/search/result/ws       Websocket to return search results as stream (future)
/search/statistic       Statistics about the results (future)

*/

package webapi

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
)

// SearchRequest is the information from the end-user for the search.
type SearchRequest struct {
	Term        string      `json:"term"`       // Search term.
	Timeout     int         `json:"timeout"`    // Timeout in seconds. 0 means default. This is the entire time the search may take. Found results are still available after this timeout.
	MaxResults  int         `json:"maxresults"` // Total number of max results. 0 means default.
	DateFrom    string      `json:"datefrom"`   // Date from, both from/to are required if set.
	DateTo      string      `json:"dateto"`     // Date to, both from/to are required if set.
	Sort        int         `json:"sort"`       // Sort order: 0 = No sorting, 1 = Relevance ASC, 2 = Relevance DESC (this should be default), 3 = Date ASC, 4 = Date DESC
	TerminateID []uuid.UUID `json:"terminate"`  // Optional: Previous search IDs to terminate. This is if the user makes a new search from the same tab. Same as first calling /search/terminate.
	TypeFilter  int         `json:"typefilter"` // 0 = No filters used, 1 = Use file type filter, 2 = Use file format filter.
	FileType    int         `json:"filetype"`   // File type such as binary, text document etc. See core.TypeX.
	FileFormat  int         `json:"fileformat"` // File format such as PDF, Word, Ebook, etc. See core.FormatX.
}

// SearchRequestResponse is the result to the initial search request
type SearchRequestResponse struct {
	ID     uuid.UUID `json:"id"`     // ID of the search job. This is used to get the results.
	Status int       `json:"status"` // Status of the search: 0 = Success (ID valid), 1 = Invalid Term, 2 = Error Max Concurrent Searches
}

// SearchResult contains the search results.
type SearchResult struct {
	Status int                  `json:"status"` // Status: 0 = Success with results, 1 = No more results available, 2 = Search ID not found, 3 = No results yet available keep trying
	Files  []apiBlockRecordFile `json:"files"`  // List of files found
}

/*
apiSearch submits a search request
Request:    POST /search with JSON SearchRequest
Result:     200 on success with JSON SearchRequestResponse
            400 on invalid JSON
*/
func apiSearch(w http.ResponseWriter, r *http.Request) {

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
		if job := JobLookup(terminate); job != nil {
			job.Terminate()
			job.Remove()
		}
	}

	job := dispatchSearch(input)

	EncodeJSON(w, r, SearchRequestResponse{Status: 0, ID: job.id})
}

/*
apiSearchResult returns results. The default limit is 100.
Request:    GET /search/result?id=[UUID]&limit=[max records]
Result:     200 with JSON structure SearchResult. Check the field status.
*/
func apiSearchResult(w http.ResponseWriter, r *http.Request) {
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
	job := JobLookup(jobID)
	if job == nil {
		EncodeJSON(w, r, SearchResult{Status: 2})
		return
	}

	// query all results
	resultFiles := job.ReturnNext(limit)

	var result SearchResult
	result.Files = []apiBlockRecordFile{}

	// loop over results
	for n := range resultFiles {
		result.Files = append(result.Files, blockRecordFileToAPI(*resultFiles[n]))
	}

	if len(result.Files) == 0 {
		result.Status = 3 // No results yet available keep trying
	}

	result.Status = 1 // No more results to expect

	EncodeJSON(w, r, result)
}

/*
apiSearchResultStream runs a websocket to return results
Request:    GET /search/result/ws?id=[UUID]&limit=[optional max records]
Result:     If successful, upgrades to a web-socket and sends JSON structure SearchResult messages
            Limit is optional. Not used if ommitted or 0.
*/ /*
func apiSearchResultStream(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	jobID, err := uuid.Parse(r.Form.Get("id"))
	if err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	limit, err := strconv.Atoi(r.Form.Get("limit"))
	useLimit := err == nil

	// look up the job
	job := JobLookup(jobID)
	if job == nil {
		EncodeJSON(w, r, SearchResult{Status: 2})
		return
	}

	// upgrade to web-socket
	conn, err := WSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// gorilla will automatically respond with "400 Bad Request", no other response is therefore necessary
		return
	}

	// loop to get new results and send out via the web socket.
	// Only exit if limit is reached if used, otherwise only if there are no result or the connection breaks.
	for {
		// query all results

		var result SearchResult
		result.Files = []apiBlockRecordFile{}

		// loop over the results

		// if no results, stall
		if len(result.Files) == 0 {
			time.Sleep(time.Millisecond * 100)
			continue
		}

		// send out the results via the web-socket
		if err := conn.WriteJSON(result); err != nil {
			conn.Close()
			return
		}

		// Check whether to continue. If the limit is used break once all done.
		if (useLimit && limit <= 0) || result.Status == 1 {
			break
		}
	}
}*/

/*
apiSearchTerminate terminates a search
Request:    GET /search/terminate?id=[UUID]
Response:   204 Empty
            400 Invalid input
            404 ID not found
*/
func apiSearchTerminate(w http.ResponseWriter, r *http.Request) {

	r.ParseForm()
	jobID, err := uuid.Parse(r.Form.Get("id"))
	if err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	// look up the job
	job := JobLookup(jobID)
	if job == nil {
		http.Error(w, "", http.StatusNotFound)
		return
	}

	// terminate and remove it from the list
	job.Terminate()
	job.Remove()

	w.WriteHeader(http.StatusNoContent)
}
