package search

import (
	"errors"
	"github.com/PeernetOfficial/core/protocol"
	"strings"
)

// GenerateIndexes This function generates various hashes based
// on the filename provided.
func GenerateIndexes(text string) ([][]byte, error) {
	var hashes [][]byte
	// returning error if the input parameter is less
	// than 3 characters
	if len(text) <= 3 {
		return nil, errors.New("text needs to be more than 3 characters")
	}
	// Appending hash for the text string
	hashes = append(hashes, protocol.HashData([]byte(text)))
	// Appending lower case hash
	hashes = append(hashes, LowerCaseHash(text))
	// Appending upper case hash
	hashes = append(hashes, UpperCaseHash(text))
	// Appending split string by word
	WordsHashes := HashByWordsSplit(text)
	for i := range WordsHashes {
		hashes = append(hashes, WordsHashes[i])
	}



	return hashes, nil
}

func Search(text string) ([]byte, error) {
	return nil, nil
}

// LowerCaseHash coverts string to lower case and returns the hash
func LowerCaseHash(name string) []byte {
     LowerCaseString := strings.ToLower(name)
	 return protocol.HashData([]byte(LowerCaseString))
}

// UpperCaseHash coverts string to upper case and returns the hash
func UpperCaseHash(name string) []byte {
    UpperCaseString := strings.ToUpper(name)
	return protocol.HashData([]byte(UpperCaseString))
}

// HashByWordsSplit splits the words in the string
// by intensifying white spaces and returns
// a multi-dimensional array of bytes and
// if the word is less than or equivalent
// to 3 characters we don't do generate
// a hash for them.
func HashByWordsSplit(name string) [][]byte {
	var hashes [][]byte
	words := strings.Fields(name)

	for i := range words {
		if len(words[i]) <= 3 {
			continue
		} else {
			hashes = append(hashes,  protocol.HashData([]byte(words[i])))
		}
	}
	return hashes
}
