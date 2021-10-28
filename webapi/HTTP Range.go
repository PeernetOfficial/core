/*
File Name:  HTTP Range.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"errors"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
)

// Fork from https://golang.org/src/net/http/fs.go

// HTTPRange represents an HTTP range
type HTTPRange struct {
	start, length int
}

// ParseRangeHeader parses a Range header string as per RFC 7233.
func ParseRangeHeader(s string, size int, noMultiRange bool) ([]HTTPRange, error) {
	if s == "" {
		return nil, nil // header not present
	}
	const b = "bytes="
	if !strings.HasPrefix(s, b) {
		return nil, errors.New("invalid range")
	}
	var ranges []HTTPRange
	for _, ra := range strings.Split(s[len(b):], ",") {
		ra = textproto.TrimString(ra)
		if ra == "" {
			continue
		}
		i := strings.Index(ra, "-")
		if i < 0 {
			return nil, errors.New("invalid range")
		}
		start, end := textproto.TrimString(ra[:i]), textproto.TrimString(ra[i+1:])
		var r HTTPRange
		if start == "" {
			if size < 0 {
				return nil, errors.New("range start relative to end not supported")
			}
			// If no start is specified, end specifies the
			// range start relative to the end of the file.
			i, err := strconv.ParseInt(end, 10, 64)
			if err != nil {
				return nil, errors.New("invalid range")
			}
			if int(i) > size {
				i = int64(size)
			}
			r.start = size - int(i)
			r.length = size - r.start
		} else {
			i, err := strconv.ParseInt(start, 10, 64)
			if err != nil || i < 0 {
				return nil, errors.New("invalid range")
			}
			if size > 0 && int(i) >= size {
				// If the range begins after the size of the content, then it does not overlap. -> always return error.
				return nil, errors.New("start out of range")
			}
			r.start = int(i)
			if end == "" {
				// If no end is specified, range extends to end of the file.
				if size < 0 {
					//return nil, errors.New("open range not supported")
					r.length = -1
				} else {
					r.length = size - r.start
				}
			} else {
				i, err := strconv.ParseInt(end, 10, 64)
				if err != nil || r.start > int(i) {
					return nil, errors.New("invalid range")
				}
				if size > 0 && int(i) >= size {
					i = int64(size - 1)
				}
				r.length = int(i) - r.start + 1
			}
		}
		ranges = append(ranges, r)
		if noMultiRange && len(ranges) > 1 {
			return nil, errors.New("multiple ranges not supported")
		}
	}
	return ranges, nil
}

// setContentLengthRangeHeader sets the appropriate Content-Length and Content-Range headers
func setContentLengthRangeHeader(w http.ResponseWriter, offset, transferSize, fileSize uint64, ranges []HTTPRange) {
	// Set the Content-Length header, always to the actual size of transferred data.
	w.Header().Set("Content-Length", strconv.FormatUint(transferSize, 10))

	// Set the Content-Range header if needed.
	if len(ranges) == 1 {
		w.Header().Set("Content-Range", "bytes "+strconv.FormatUint(offset, 10)+"-"+strconv.FormatUint(offset+transferSize-1, 10)+"/"+strconv.FormatUint(fileSize, 10))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}
}
