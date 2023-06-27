/*
File name:  Normalizing.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Normalizing text so that it can be hashed.
*/

package search

import (
	"path"
	"strings"
)

// sanitizeGeneric sanitizes the text. It intentionally does not lowercase the text so CamelCase can be detcted later.
func sanitizeGeneric(filename string) string {
	filename = strings.ToValidUTF8(filename, "")
	filename = strings.TrimSpace(filename)

	return filename
}

// sanitizeInputTerm sanitizes a search term provided by the end user. It includes the generic rules that are done on indexing.
func sanitizeInputTerm(inputTerm string) (outputTerm string, isExact, isWildcard bool) {
	inputTerm = sanitizeGeneric(inputTerm)

	// detect and remove quotes at the beginning and end
	isExact = false
	if len(inputTerm) >= 2 && ((strings.HasPrefix(inputTerm, "\"") && strings.HasSuffix(inputTerm, "\"")) || (strings.HasPrefix(inputTerm, "'") && strings.HasSuffix(inputTerm, "'"))) {
		inputTerm = inputTerm[1 : len(inputTerm)-1]
		isExact = true
	}

	isWildcard = strings.Contains(inputTerm, "*")

	return inputTerm, isExact, isWildcard
}

func filenameRemoveExtension(filename string) string {
	return strings.TrimSuffix(filename, path.Ext(filename))
}
