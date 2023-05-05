package webapi

import (
	"bytes"
	"fmt"
	"github.com/PeernetOfficial/core/blockchain"
	"net/http"
	"strconv"
)

// SearchResultMergedDirectory contains results for the merged directory.
type SearchResultMergedDirectory struct {
	Status    int         `json:"status"`    // Status: 0 = Success with results, 1 = No more results available, 2 = Search ID not found, 3 = No results yet available keep trying
	Files     []apiFile   `json:"files"`     // List of files found
	Statistic interface{} `json:"statistic"` // Statistics of all results (independent from applied filters), if requested. Only set if files are returned (= if statistics changed). See SearchStatisticData.
}

/*
apiMergeDirectory Shows the recent files of peers that shared
the same file as the one provided in the GET request.
Currently searches through Memory for Nodes currently
identified in the network and then checks if the files
they shared match with the hash that is provided
in the search parameter and the queries the recent
file that node shared and then returns that result
back.

Request:    GET /merge/directory with JSON structure apiMergeDirectory
Response:   200 with JSON structure SearchResultMergedDirectory

	400 if invalid input
*/
func (api *WebapiInstance) apiMergeDirectory(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	offset, _ := strconv.Atoi(r.Form.Get("offset"))
	limit, err := strconv.Atoi(r.Form.Get("limit"))
	if err != nil {
		limit = 100
	}
	// ID fields for results for a specific node ID.
	//NodeId, _ := DecodeBlake3Hash(r.Form.Get("node"))
	hash, _ := DecodeBlake3Hash(r.Form.Get("hash"))

	fileType, err := strconv.Atoi(r.Form.Get("type"))
	if err != nil {
		fileType = -1
	}

	result := api.ExploreFileSharedByNodeThatSharedSimilarFile(fileType, limit, offset, hash, true)

	EncodeJSON(api.Backend, w, r, result)
}

// ExploreFileSharedByNodeThatSharedSimilarFile lists files shared by a nodes which share the same file as a common point
// Currently this is a greedy search and requires work for optimization
func (api *WebapiInstance) ExploreFileSharedByNodeThatSharedSimilarFile(fileType int, limit, offset int, hash []byte, nodeIDState bool) *SearchResultMergedDirectory {
	// lookup all NodeID blockchains which have the similar hash
	// do a search to get all the node IDs sharing the particular file
	var NodeIDs [][]byte
	NodeIDs, _ = api.Backend.SearchIndex.SearchNodeIDBasedOnHash(hash)

	// Does a greedy search to find node serving similar files
	api.GreedySearchMergeDirection(&NodeIDs, fileType, hash)

	var result SearchResultMergedDirectory

	for i := range NodeIDs {
		resultFiles := api.queryRecentShared(api.Backend, fileType, uint64(limit*20/100), uint64(offset), uint64(limit), NodeIDs[i], nodeIDState)

		// loop over results
		for n := range resultFiles {
			result.Files = append(result.Files, blockRecordFileToAPI(resultFiles[n]))
		}

		//

	}

	result.Status = 1 // No more results to expect

	return &result
}

// GreedySearchMergeDirection This function is implemented since the local index tables do not
// always consist of the required hashes of the NodeIDs.
func (api *WebapiInstance) GreedySearchMergeDirection(nodeID *[][]byte, fileType int, hash []byte) {

	// get all NodeIDs
	peerList := api.Backend.PeerlistGet()

	// search with AllNodes which have a match of the NodeID.
	for _, peer := range peerList {
		if peer.BlockchainHeight == 0 {
			continue
		}

		var filesFromPeer uint64

		// decode blocks from top down
		for blockN := peer.BlockchainHeight - 1; blockN > 0; blockN-- {
			blockDecoded, _, found, _ := api.Backend.ReadBlock(peer.PublicKey, peer.BlockchainVersion, blockN)
			if !found {
				continue
			}

			for _, record := range blockDecoded.RecordsDecoded {
				if file, ok := record.(blockchain.BlockRecordFile); ok && isFileTypeMatchBlock(&file, fileType) {
					// add the tags 'Shared By Count' and 'Shared By GeoIP'
					file.Tags = append(file.Tags, blockchain.TagFromNumber(blockchain.TagSharedByCount, 1))
					if latitude, longitude, valid := api.Peer2GeoIP(peer); valid {
						sharedByGeoIP := fmt.Sprintf("%.4f", latitude) + "," + fmt.Sprintf("%.4f", longitude)
						file.Tags = append(file.Tags, blockchain.TagFromText(blockchain.TagSharedByGeoIP, sharedByGeoIP))
					}

					// if both the hashes match
					if bytes.Equal(file.Hash, hash) {
						// set the Tags needed for the filter parameter
						//tags = file.Tags

						// found a new file! append.
						filesFromPeer++

						*nodeID = append(*nodeID, file.NodeID)
					}
				}
			}

		}
	}

}
