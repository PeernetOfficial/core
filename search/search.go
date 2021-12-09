package search

import (
	"errors"
	"github.com/PeernetOfficial/core"
	"github.com/PeernetOfficial/core/dht"
	"github.com/PeernetOfficial/core/protocol"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	"strings"
	"unicode"
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

	// Normalizes string provided in the parameter
	normalizedStr, err := NormalizeWords(text)
	if err != nil {
		return nil, err
	}

	// normalized hash
	normalizedHash := protocol.HashData([]byte(normalizedStr))

	// Appending hash for the text string
	hashes = append(hashes, protocol.HashData([]byte(text)))
	// Appending normalized hash
	hashes = append(hashes, normalizedHash)
	// Appending lower case hash
	//hashes = append(hashes, LowerCaseHash(normalizedStr))
	// Appending upper case hash
	hashes = append(hashes, UpperCaseHash(normalizedStr))
	// Appending split string by word
	WordsHashes := WordsSplitString(normalizedStr)
	for i := range WordsHashes {
		if len(WordsHashes[i]) <= 3 {
			continue
		}
		hashes = append(hashes, protocol.HashData([]byte(WordsHashes[i])))
		// Appending lower case for the specific word
		//hashes = append(hashes, LowerCaseHash(WordsHashes[i]))
		// Appending upper case for the specific word
		hashes = append(hashes, UpperCaseHash(WordsHashes[i]))
	}

	err = core.InsertIndexRows(hashes, text)
	if err != nil {
		return nil, err
	}

	return hashes, nil
}

// Search This function returns results for
// the text provided
func Search(hash []byte) ([]string, error) {
	// Local search
	texts, err := core.SearchTextBasedOnHash(hash)
	if err != nil {
		return nil, err
	}

	return texts, nil
}

// SearchDHT Queries the DHT to search for a certain text
func SearchDHT(text string, dht *dht.DHT) ([]string, error) {
	return nil, nil
}

// RemoveIndexesHash Deletes generated hashes
func RemoveIndexesHash(hash []byte) error {
	err := core.DeleteIndexesBasedOnHash(hash)
	if err != nil {
		return err
	}
	return nil
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

// NormalizeWords Normalizes and sanitizes string passed as the parameter
func NormalizeWords(text string) (string, error) {
	// Make the string lower case
	text = strings.ToLower(text)

	// change spaces
	wordsSplit := WordsSplitString(text)

	// Append string with words with a single space
	var textWithSpaces string
	for i := range wordsSplit {
		if i == 0 {
			textWithSpaces = wordsSplit[i]
		} else {
			textWithSpaces = textWithSpaces + " " + wordsSplit[i]
		}
	}

	// Replace _ with a space
	textWithSpaces = strings.ReplaceAll(textWithSpaces, "_", " ")
	// Replace - with a space
	textWithSpaces = strings.ReplaceAll(textWithSpaces, "-", " ")

	// Removing diacritics
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	result, _, err := transform.String(t, textWithSpaces)
	if err != nil {
		return "", err
	}

	return result, nil
}

// WordsSplitString splits the words in the string
// by intensifying white spaces and returns
// a string of bytes
func WordsSplitString(name string) []string {
	words := strings.Fields(name)
	return words
}
