/*
File Name:  Search Dispatch.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"bytes"
	"fmt"
	"time"

	"github.com/PeernetOfficial/core/blockchain"
)

func (api *WebapiInstance) dispatchSearch(input SearchRequest) (job *SearchJob) {
	Timeout := input.Parse()
	Filter := input.ToSearchFilter()

	// create the search job
	job = api.CreateSearchJob(Timeout, input.MaxResults, Filter)

	// todo: create actual search clients!
	job.Status = SearchStatusLive

	go job.localSearch(api, input.Term)

	api.RemoveJobDefer(job, job.timeout+time.Minute*10)

	return job
}

func (job *SearchJob) localSearch(api *WebapiInstance, term string) {
	if api.backend.SearchIndex == nil {
		job.Status = SearchStatusNoIndex
		return
	}

	results := api.backend.SearchIndex.Search(term)

	job.ResultSync.Lock()

resultLoop:
	for _, result := range results {
		file, _, found, err := api.backend.ReadFile(result.PublicKey, result.BlockchainVersion, result.BlockNumber, result.FileID)
		if err != nil || !found {
			continue
		}

		// Deduplicate based on file hash from the same peer.
		for n := range job.AllFiles {
			if bytes.Equal(job.AllFiles[n].Hash, file.Hash) && bytes.Equal(job.AllFiles[n].NodeID, file.NodeID) {
				continue resultLoop
			}
		}

		if bytes.Equal(file.NodeID, api.backend.SelfNodeID()) {
			// Indicates data from the current user.
			file.Tags = append(file.Tags, blockchain.TagFromNumber(blockchain.TagSharedByCount, 1))
		} else if peer := api.backend.NodelistLookup(file.NodeID); peer != nil {
			// add the tags 'Shared By Count' and 'Shared By GeoIP'
			file.Tags = append(file.Tags, blockchain.TagFromNumber(blockchain.TagSharedByCount, 1))
			if latitude, longitude, valid := api.Peer2GeoIP(peer); valid {
				sharedByGeoIP := fmt.Sprintf("%.4f", latitude) + "," + fmt.Sprintf("%.4f", longitude)
				file.Tags = append(file.Tags, blockchain.TagFromText(blockchain.TagSharedByGeoIP, sharedByGeoIP))
			}
		}

		// new result
		newFile := blockRecordFileToAPI(file)

		job.Files = append(job.Files, &newFile)
		job.AllFiles = append(job.AllFiles, &newFile)
		job.requireSort = true
		job.statsAdd(&newFile)
	}

	job.Status = SearchStatusTerminated

	job.ResultSync.Unlock()
	job.Terminate()
}
