/*
File Name:  Merkle Tree.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Generates the merkle tree based on input data.
In case of uneven number of fragments, the last fragment will be hashed against the top hash of all the left tree to create the merkle root hash.
*/

package fragment

import (
	"errors"
	"io"

	"lukechampine.com/blake3"
)

// MerkleTree represents an entire merkle tree
type MerkleTree struct {
	// information about the original file
	fileSize      uint64
	fragmentSize  uint64
	fragmentCount uint64

	// list of hashes
	fragmentHashes [][]byte   // List of hashes for each fragment
	rootHash       []byte     // Root hash.
	middleHashes   [][][]byte // All hashes in the middle, bottom up.
}

// NewMerkleTree creates a new merkle tree from the input
func NewMerkleTree(fileSize, fragmentSize uint64, reader io.Reader) (tree *MerkleTree, err error) {
	if fragmentSize == 0 {
		return nil, errors.New("invalid fragment size")
	}

	tree = &MerkleTree{
		fileSize:      fileSize,
		fragmentSize:  fragmentSize,
		fragmentCount: fileSizeToFragmentCount(fileSize, fragmentSize),
	}

	// Special case: No fragments, in case of empty data.
	if tree.fragmentCount == 0 {
		hash := blake3.Sum256(nil)
		tree.rootHash = hash[:]

		return tree, nil
	} else if tree.fragmentCount == 1 {
		// Special case: Single fragment.
		data := make([]byte, fileSize)
		if _, err := io.ReadAtLeast(reader, data, int(fileSize)); err != nil {
			return nil, err
		}

		hash := blake3.Sum256(data)
		tree.rootHash = hash[:]

		return tree, nil
	}

	// calculate the hash per fragment
	data := make([]byte, fragmentSize)
	remaining := fileSize

	for n := uint64(0); n < tree.fragmentCount; n++ {
		if fragmentSize > remaining {
			fragmentSize = remaining
		}

		if _, err := io.ReadAtLeast(reader, data, int(fragmentSize)); err != nil {
			return nil, err
		}

		// hash the fragment
		hash := blake3.Sum256(data[:fragmentSize])

		tree.fragmentHashes = append(tree.fragmentHashes, hash[:])

		remaining -= fragmentSize
	}

	// calculate the intermediate hashes
	tree.calculateMiddleHashes(0)

	return tree, nil
}

func fileSizeToFragmentCount(fileSize, fragmentSize uint64) (count uint64) {
	return (fileSize + fragmentSize - 1) / fragmentSize
}

func (tree *MerkleTree) calculateMiddleHashes(level uint64) {
	if len(tree.fragmentHashes) == 0 {
		return
	}

	var newHashes, inputHashes [][]byte

	if level == 0 {
		inputHashes = tree.fragmentHashes
	} else {
		inputHashes = tree.middleHashes[level-1]
	}

	for n := 0; n+1 <= len(inputHashes)-1; n += 2 {
		newHashes = append(newHashes, calculateMiddleHash(inputHashes[n], inputHashes[n+1]))
	}

	// Uneven leafs? in this case the new hash is just a copy of the uneven one. No point in artifically recalcualting it with itself like Bitcoin does.
	// For other possible implementations see https://medium.com/coinmonks/merkle-trees-concepts-and-use-cases-5da873702318.
	if len(inputHashes)%2 != 0 {
		newHashes = append(newHashes, inputHashes[len(inputHashes)-1])
	}

	if len(newHashes) == 1 {
		// Only one hash generated.
		tree.rootHash = newHashes[0]
	} else if len(newHashes) > 1 {
		tree.middleHashes = append(tree.middleHashes, newHashes)

		tree.calculateMiddleHashes(level + 1)
	}
}

func calculateMiddleHash(hash1 []byte, hash2 []byte) (newHash []byte) {
	var data []byte
	data = append(data, hash1...)
	data = append(data, hash2...)

	hash := blake3.Sum256(data)

	return hash[:]
}

// Export/Import of the merkle tree structure:
// TODO

// Export stores the tree as blob
func (tree *MerkleTree) Export() (data []byte) {
	return nil
}

// Import reads the tree from the input data
func (tree *MerkleTree) Import(data []byte) {

}
