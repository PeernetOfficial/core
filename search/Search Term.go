/*
File Name:  Search Term.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package search

func (index *SearchIndexStore) Search(term string) (results []SearchIndexRecord) {
	termS, isExact, _ := sanitizeInputTerm(term)

	if len(termS) < wordMinLength {
		return
	}

	// start with exact search
	hashExact := hashWord(termS)
	if hashExact != nil {
		records, _ := index.LookupHash(hashExact)
		results = append(results, setSearchIndexFields(records, termS)...)
	}

	// exact search only?
	if isExact {
		return
	}

	// break up the term into hashes
	hashes := make(map[[32]byte]string)

	text2Hashes(termS, hashes)

	// The exact search was already performed, exclude it.
	hashMapDelete(hashExact, hashes)

	// look up the hashes!
	for hash, keyword := range hashes {
		records, _ := index.LookupHash(hash[:])
		results = append(results, setSearchIndexFields(records, keyword)...)
	}

	return
}

// setSearchIndexFields sets certain fields in the index record for adding context in the output
func setSearchIndexFields(records []SearchIndexRecord, word string) []SearchIndexRecord {
	for n := range records {
		records[n].Word = word
	}
	return records
}
