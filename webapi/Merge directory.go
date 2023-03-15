package webapi

import (
	"net/http"
	"strconv"
)

// SearchResultMergedDirectory contains results for the merged directory.
type SearchResultMergedDirectory struct {
	Status    int         `json:"status"`    // Status: 0 = Success with results, 1 = No more results available, 2 = Search ID not found, 3 = No results yet available keep trying
	Files     [][]apiFile `json:"files"`     // List of files found
	Statistic interface{} `json:"statistic"` // Statistics of all results (independent from applied filters), if requested. Only set if files are returned (= if statistics changed). See SearchStatisticData.
}

func (api *WebapiInstance) apiMergeDirectory(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	offset, _ := strconv.Atoi(r.Form.Get("offset"))
	limit, err := strconv.Atoi(r.Form.Get("limit"))
	if err != nil {
		limit = 100
	}
	// ID fields for results for a specific node ID.
	NodeId, _ := DecodeBlake3Hash(r.Form.Get("node"))
	hash, _ := DecodeBlake3Hash(r.Form.Get("hash"))

	fileType, err := strconv.Atoi(r.Form.Get("type"))
	if err != nil {
		fileType = -1
	}

	result := api.ExploreFileSharedByNodeThatSharedSimilarFile(fileType, limit, offset, NodeId, hash, true)

	EncodeJSON(api.Backend, w, r, result)
}

// ExploreFileSharedByNodeThatSharedSimilarFile lists files shared by a nodes which share the same file as a common point
// Currently this is a greedy search and requires work for optimization
func (api *WebapiInstance) ExploreFileSharedByNodeThatSharedSimilarFile(fileType int, limit, offset int, nodeID []byte, hash []byte, nodeIDState bool) *SearchResultMergedDirectory {
	// lookup all NodeID blockchains which have the similar hash
	// do a search to get all the node IDs sharing the particular file
	NodeIDs, _ := api.Backend.SearchIndex.SearchNodeIDBasedOnHash(nodeID)

	var result SearchResultMergedDirectory

	for i := range NodeIDs {
		resultFiles := api.queryRecentShared(api.Backend, fileType, uint64(limit*20/100), uint64(offset), uint64(limit), NodeIDs[i], nodeIDState)

		var ApiFile []apiFile

		// loop over results
		for n := range resultFiles {
			ApiFile = append(ApiFile, blockRecordFileToAPI(resultFiles[n]))
		}

		result.Files = append(result.Files, ApiFile)

	}

	result.Status = 1 // No more results to expect

	return &result
}
