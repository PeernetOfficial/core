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

// text2Hashes creates hashes from words in the text. Text may be CamelCased.
// Text must be already validated for valid UTF8 when calling this function.
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

		// CamelCase word detection
		for _, word2 := range CamelCaseSplit(word) {
			if word2 != word {
				hashWordMap(word2, hashes)
			}
		}
	}
}

// filename2Hashes creates hashes based on the filename and folder.
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

// hashWordMap hashes a word and stores it on the map. This immediately deduplicated hashes. It always lowercases the word.
func hashWordMap(word string, hashes map[[32]byte]string) {
	word = strings.TrimSpace(strings.ToLower(word))
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

// hashWord hashes a single word. It returns nil if not suitable. It always lowercases the word.
func hashWord(word string) (hash []byte, wordHashed string) {
	word = strings.TrimSpace(strings.ToLower(word))
	if len(word) < wordMinLength {
		return
	}

	hashB := blake3.Sum256([]byte(word))
	return hashB[:], word
}

// fork from https://github.com/fatih/camelcase/blob/master/camelcase.go

// Split splits the camelcase word and returns a list of words. It also
// supports digits. Both lower camel case and upper camel case are supported.
// For more info please check: http://en.wikipedia.org/wiki/CamelCase
//
// Examples
//
//	"" =>                     [""]
//	"lowercase" =>            ["lowercase"]
//	"Class" =>                ["Class"]
//	"MyClass" =>              ["My", "Class"]
//	"MyC" =>                  ["My", "C"]
//	"HTML" =>                 ["HTML"]
//	"PDFLoader" =>            ["PDF", "Loader"]
//	"AString" =>              ["A", "String"]
//	"SimpleXMLParser" =>      ["Simple", "XML", "Parser"]
//	"vimRPCPlugin" =>         ["vim", "RPC", "Plugin"]
//	"GL11Version" =>          ["GL", "11", "Version"]
//	"99Bottles" =>            ["99", "Bottles"]
//	"May5" =>                 ["May", "5"]
//	"BFG9000" =>              ["BFG", "9000"]
//	"BöseÜberraschung" =>     ["Böse", "Überraschung"]
//	"Two  spaces" =>          ["Two", "  ", "spaces"]
//	"BadUTF8\xe2\xe2\xa1" =>  ["BadUTF8\xe2\xe2\xa1"]
//
// Splitting rules
//
//  1. If string is not valid UTF-8, return it without splitting as -> removed in this fork.
//     single item array.
//  2. Assign all unicode characters into one of 4 sets: lower case
//     letters, upper case letters, numbers, and all other characters.
//  3. Iterate through characters of string, introducing splits
//     between adjacent characters that belong to different sets.
//  4. Iterate through array of split strings, and if a given string
//     is upper case:
//     if subsequent string is lower case:
//     move last character of upper case string to beginning of
//     lower case string
func CamelCaseSplit(src string) (entries []string) {
	entries = []string{}
	var runes [][]rune
	lastClass := 0
	class := 0
	// split into fields based on class of unicode character
	for _, r := range src {
		switch true {
		case unicode.IsLower(r):
			class = 1
		case unicode.IsUpper(r):
			class = 2
		case unicode.IsDigit(r):
			class = 3
		default:
			class = 4
		}
		if class == lastClass {
			runes[len(runes)-1] = append(runes[len(runes)-1], r)
		} else {
			runes = append(runes, []rune{r})
		}
		lastClass = class
	}
	// handle upper case -> lower case sequences, e.g.
	// "PDFL", "oader" -> "PDF", "Loader"
	for i := 0; i < len(runes)-1; i++ {
		if unicode.IsUpper(runes[i][0]) && unicode.IsLower(runes[i+1][0]) {
			runes[i+1] = append([]rune{runes[i][len(runes[i])-1]}, runes[i+1]...)
			runes[i] = runes[i][:len(runes[i])-1]
		}
	}
	// construct []string from results
	for _, s := range runes {
		if len(s) > 0 {
			entries = append(entries, string(s))
		}
	}
	return
}
