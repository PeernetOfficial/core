/*
File Name:  Merkle Tree.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Generates the merkle tree based on input data.
In case of uneven number of fragments, the last uneven fragment is moved up a level.
*/

package fragment

import (
	"bytes"
	"encoding/binary"
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
	middleHashes   [][][]byte // All hashes in the middle, bottom up.
	rootHash       []byte     // Root hash.
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

// CreateVerification returns the verification hashes for the given fragment number. The root hash itself is not included.
// The result might be empty if there is no or a single fragment.
// Each verification hash has a preceding left (= 0)/right (= 1) indicator that indicates where the verification is positioned.
// This makes the algorithm future proof, in case uneven leafs will be handled differently.
func (tree *MerkleTree) CreateVerification(fragment uint64) (verificationHashes [][]byte) {
	// 0 fragments: Empty data.
	// 1 fragment: The hash of the fragment is the root hash.
	if tree.fragmentCount <= 1 {
		return nil
	} else if fragment >= tree.fragmentCount {
		// invalid fragment index
		return nil
	}

	// first hash it he neighbor fragment hash, if available
	if fragment == tree.fragmentCount-1 && fragment%2 == 0 {
	} else if fragment%2 == 0 {
		verificationHashes = append(verificationHashes, append([]byte{1}, tree.fragmentHashes[fragment+1]...))
	} else {
		verificationHashes = append(verificationHashes, append([]byte{0}, tree.fragmentHashes[fragment-1]...))
	}

	// go through all middle hash levels
	for n := 0; n < len(tree.middleHashes); n++ {
		fragment = fragment / 2

		if fragment == uint64(len(tree.middleHashes[n])-1) && fragment%2 == 0 {
		} else if fragment%2 == 0 {
			verificationHashes = append(verificationHashes, append([]byte{1}, tree.middleHashes[n][fragment+1]...))
		} else {
			verificationHashes = append(verificationHashes, append([]byte{0}, tree.middleHashes[n][fragment-1]...))
		}
	}

	return
}

// MerkleVerify validates the hashed data against the verification hashes and the known root hash.
func MerkleVerify(rootHash []byte, dataHash []byte, verificationHashes [][]byte) (valid bool) {
	for _, verifyHash := range verificationHashes {
		if verifyHash[0] == 0 {
			dataHash = calculateMiddleHash(verifyHash[1:], dataHash)
		} else {
			dataHash = calculateMiddleHash(dataHash, verifyHash[1:])
		}
	}

	return bytes.Equal(rootHash, dataHash)
}

/*
Export/Import of the merkle tree structure:

Offset  Size        Info
0       8           File Size
8       8           Fragment Size
16      32          Merkle Root Hash
48      32 * n      Fragment Hashes
?       32 * n      Middle Hashes

*/

const merkleTreeFileHeaderSize = 8 + 8 + 32

// calculateTotalHashCount returns the total number of fragment and middle hashes needed for the given count of fragments
func calculateTotalHashCount(fragmentCount uint64) (count uint64) {
	// Special case no or 1 fragment: None needed, since the fragment hash is directly stored as root hash.
	if fragmentCount <= 1 {
		return 0
	}

	// Equal count of fragment hashes needed
	count = fragmentCount

	// Calculate middle hashes number
	for countHashesLast := fragmentCount; ; {
		countMiddleNew := (countHashesLast + 1) / 2 // round up
		if countMiddleNew <= 1 {
			break
		}

		count += countMiddleNew
		countHashesLast = countMiddleNew
	}

	return count
}

// Export stores the tree as blob
func (tree *MerkleTree) Export() (data []byte) {
	data = make([]byte, merkleTreeFileHeaderSize+calculateTotalHashCount(tree.fragmentCount)*32)

	// header
	binary.LittleEndian.PutUint64(data[0:8], tree.fileSize)
	binary.LittleEndian.PutUint64(data[8:16], tree.fragmentSize)
	copy(data[16:16+32], tree.rootHash)

	// fragment hashes
	offset := 48
	for _, hash := range tree.fragmentHashes {
		copy(data[offset:offset+32], hash)
		offset += 32
	}

	// middle hashes
	for n := 0; n < len(tree.middleHashes); n++ {
		for _, hash := range tree.middleHashes[n] {
			copy(data[offset:offset+32], hash)
			offset += 32
		}
	}

	return data[:offset]
}

// Import reads the tree from the input data
func ImportMerkleTree(data []byte) (tree *MerkleTree) {
	// Read the header. Enforce the minimum size.
	if len(data) < 8+8+32 {
		return nil
	}

	tree = &MerkleTree{
		fileSize:     binary.LittleEndian.Uint64(data[0:8]),
		fragmentSize: binary.LittleEndian.Uint64(data[8:16]),
	}
	tree.fragmentCount = fileSizeToFragmentCount(tree.fileSize, tree.fragmentSize)
	tree.rootHash = data[16 : 16+32]

	if tree.fragmentCount <= 1 {
		return tree
	}

	// verify size
	if uint64(len(data)) < merkleTreeFileHeaderSize+calculateTotalHashCount(tree.fragmentCount)*32 {
		return nil
	}

	// fragment hashes
	offset := 48
	for n := 0; n < int(tree.fragmentCount); n++ {
		hash := data[offset : offset+32]
		tree.fragmentHashes = append(tree.fragmentHashes, hash)
		offset += 32
	}

	// middle hashes
	n := tree.fragmentCount / 2
	if tree.fragmentCount > 2 && tree.fragmentCount%2 != 0 {
		n++
	}

	for ; n > 1; n = n / 2 {
		var hashList [][]byte
		for m := uint64(0); m < n; m++ {
			hash := data[offset : offset+32]
			hashList = append(hashList, hash)
			offset += 32
		}

		tree.middleHashes = append(tree.middleHashes, hashList)
		if len(hashList)%2 != 0 {
			n++
		}
	}

	return
}
