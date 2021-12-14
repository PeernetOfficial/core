/*
File Name:  Text 2 Hash.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package search

import (
	"strings"
	"unicode"

	"lukechampine.com/blake3"
)

// Words must be the minimum length to be indexed.
const wordMinLength = 3

func text2Hashes(text string, hashes map[[32]byte]string) {
	words := strings.FieldsFunc(text, func(char rune) bool {
		if unicode.IsSpace(char) {
			return true
		}
		return strings.ContainsAny(string(char), "+-._()[],–")
	})

	for _, word := range words {
		// remove hash tag prefix
		word = strings.TrimPrefix(word, "#")

		hashWordMap(word, hashes)
	}
}

func filename2Hashes(filename, folder string, hashes map[[32]byte]string) {
	if len(filename) < wordMinLength {
		return
	}

	// First hash is created on the entire filename including extension. This is done for perfect matches.
	hashWordMap(filename, hashes)

	// Hash the filename without extension (and proceed without extension)
	filename = filenameRemoveExtension(filename)
	hashWordMap(filename, hashes)

	// Hash each individual word of the filename and directory
	text2Hashes(filename, hashes)

	folder = strings.ReplaceAll(folder, "\\", " ")
	folder = strings.ReplaceAll(folder, "/", " ")
	text2Hashes(folder, hashes)
}

// hashWordMap hashes a word and stores it on the map. This immediately deduplicated hashes.
func hashWordMap(word string, hashes map[[32]byte]string) {
	word = strings.TrimSpace(word)
	if len(word) < wordMinLength {
		return
	}

	hashes[blake3.Sum256([]byte(word))] = word
}

// hashMapDelete deletes a hash from the list if the hash is valid
func hashMapDelete(hash []byte, hashes map[[32]byte]string) {
	if len(hash) == 0 {
		return
	}

	var hashB [32]byte
	copy(hashB[:], hash)

	delete(hashes, hashB)
}

// hashWord hashes a single word. It returns nil if not suitable.
func hashWord(word string) (hash []byte) {
	word = strings.TrimSpace(word)
	if len(word) < wordMinLength {
		return
	}

	hashB := blake3.Sum256([]byte(word))
	return hashB[:]
}
