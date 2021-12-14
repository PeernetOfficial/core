/*
File Name:  Search Dispatch.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/
package webapi

import (
	"bytes"
	"time"

	"github.com/PeernetOfficial/core"
	"github.com/PeernetOfficial/core/blockchain"
)

func (api *WebapiInstance) dispatchSearch(input SearchRequest) (job *SearchJob) {
	Timeout := input.Parse()
	Filter := input.ToSearchFilter()

	// create the search job
	job = CreateSearchJob(Timeout, input.MaxResults, Filter)

	// todo: create actual search clients!

	go job.localSearch(api.backend, input.Term)

	job.RemoveDefer(job.timeout + time.Minute*10)

	return job
}

func (job *SearchJob) localSearch(backend *core.Backend, term string) {
	if backend.SearchIndex == nil || backend.GlobalBlockchainCache == nil {
		return
	}

	results := backend.SearchIndex.Search(term)

	job.ResultSync.Lock()

resultLoop:
	for _, result := range results {
		file, _, found, err := backend.GlobalBlockchainCache.ReadFile(result.PublicKey, result.BlockchainVersion, result.BlockNumber, result.FileID)
		if err != nil || !found {
			continue
		}

		// Deduplicate based on file hash from the same peer.
		for n := range job.AllFiles {
			if bytes.Equal(job.AllFiles[n].Hash, file.Hash) && bytes.Equal(job.AllFiles[n].NodeID, file.NodeID) {
				continue resultLoop
			}
		}

		if peer := core.NodelistLookup(file.NodeID); peer != nil {
			file.Tags = append(file.Tags, blockchain.TagFromNumber(blockchain.TagSharedByCount, 1))
		}

		// new result
		newFile := blockRecordFileToAPI(file)

		job.Files = append(job.Files, &newFile)
		job.AllFiles = append(job.AllFiles, &newFile)
		job.requireSort = true
		job.statsAdd(&newFile)
	}

	job.ResultSync.Unlock()
	job.Terminate()
}
