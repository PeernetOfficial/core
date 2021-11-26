package fragment

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"testing"
)

func TestFragment0(t *testing.T) {
	dataSize := uint64(11*1024*1024 + 100)

	data := make([]byte, dataSize)

	if _, err := io.ReadFull(rand.Reader, data); err != nil {
		return
	}

	fragmentSize := CalculateFragmentSize(dataSize)

	tree, err := NewMerkleTree(dataSize, fragmentSize, bytes.NewBuffer(data))

	if err != nil {
		fmt.Printf("Error creating merkle tree: %v\n", err)
		return
	}

	printMerkleTree(tree)
}

func printMerkleTree(tree *MerkleTree) {
	fmt.Printf("File size: %d\n", tree.fileSize)
	fmt.Printf("Fragment size: %d\n", tree.fragmentSize)
	fmt.Printf("Fragment count: %d\n", tree.fragmentCount)

	fmt.Printf("Merkle root hash: %s\n", hex.EncodeToString(tree.rootHash))

	for n := 0; n < len(tree.fragmentHashes); n++ {
		fmt.Printf("Fragment %d: %s\n", n, hex.EncodeToString(tree.fragmentHashes[n]))
	}
	for n := 0; n < len(tree.middleHashes); n++ {
		for m := 0; m < len(tree.middleHashes[n]); m++ {
			fmt.Printf("Middle hash [level %d] %d: %s\n", n, m, hex.EncodeToString(tree.middleHashes[n][m]))
		}
	}
}
