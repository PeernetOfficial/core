/*
File Name:  Sanitize.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package sanitize

import (
	"path"
	"strings"
	"unicode/utf8"
)

const PATH_MAX_LENGTH = 32767 // Windows Maximum Path Length for UNC paths

// PathDirectory sanitizes a directory path (without filename)
func PathDirectory(directory string) string {
	// Enforced forward slashes as directory separator and clean the path.
	directory = strings.ReplaceAll(directory, "\\", "/")
	directory = path.Clean(directory)

	// No slash at the beginning and end to save space.
	directory = strings.Trim(directory, "/")

	// Enforce max length.
	if len(directory) > PATH_MAX_LENGTH {
		directory = directory[:PATH_MAX_LENGTH]
	}

	return directory
}

// PathFile sanitizes the filename.
func PathFile(filename string) string {
	// Enforce max filename length.
	if len(filename) > PATH_MAX_LENGTH {
		filename = filename[:PATH_MAX_LENGTH]
	}

	return filename
}

// Username sanitizes the username.
func Username(input string) string {
	if !utf8.ValidString(input) {
		return "<invalid encoding>"
	}

	input = strings.TrimSpace(input)
	input = strings.ReplaceAll(input, "\n", " ")
	input = strings.ReplaceAll(input, "\r", "")

	// Max length for sanitized version is 36, resembling the limit from StackOverflow.
	if len(input) > 36 {
		input = input[:36]
	}

	return input
}
