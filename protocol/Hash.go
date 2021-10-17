/*
File Name:  Hash.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package protocol

import "lukechampine.com/blake3"

// HashData abstracts the hash function.
func HashData(data []byte) (hash []byte) {
	hash32 := blake3.Sum256(data)
	return hash32[:]
}

// HashSize is blake3 hash digest size = 256 bits
const HashSize = 32
