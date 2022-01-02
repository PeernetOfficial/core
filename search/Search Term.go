/*
File Name:  Search Term.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package search

import (
	"github.com/google/uuid"
)

func (index *SearchIndexStore) Search(term string) (results []SearchIndexRecord) {
	if index == nil { // Search index may not be available.
		return nil
	}

	termS, isExact, _ := sanitizeInputTerm(term)

	if len(termS) < wordMinLength {
		return
	}

	resultMap := make(map[uuid.UUID]*SearchIndexRecord)
	resultMapToSlice := func() (results []SearchIndexRecord) {
		for _, result := range resultMap {
			results = append(results, *result)
		}
		return results
	}

	// start with exact search
	hashExact, wordH := hashWord(termS)
	if hashExact != nil {
		index.LookupHash(SearchSelector{Hash: hashExact, Word: wordH, ExactSearch: true}, resultMap)
	}

	// exact search only?
	if isExact {
		return resultMapToSlice()
	}

	// break up the term into hashes
	hashes := make(map[[32]byte]string)

	text2Hashes(termS, hashes)

	// The exact search was already performed, exclude it.
	hashMapDelete(hashExact, hashes)

	// look up the hashes!
	for hash, keyword := range hashes {
		index.LookupHash(SearchSelector{Hash: hash[:], Word: keyword}, resultMap)
	}

	return resultMapToSlice()
}
