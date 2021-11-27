package fragment

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"testing"

	"lukechampine.com/blake3"
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

	// Validate all hashes.
	for n := uint64(0); n < tree.fragmentCount; n++ {
		verificationHashes := tree.CreateVerification(n)

		dataSize := tree.fragmentSize
		if n == tree.fragmentCount-1 {
			dataSize = tree.fileSize - n*tree.fragmentSize
		}
		dataHash := blake3.Sum256(data[n*tree.fragmentSize : n*tree.fragmentSize+dataSize])

		valid := MerkleVerify(tree.rootHash, dataHash[:], verificationHashes)

		fmt.Printf("Validate fragment %d: %t\n", n, valid)
		if !valid {
			for m := 0; m < len(verificationHashes); m++ {
				fmt.Printf("-> Middle hash [level %d]: %s\n", m-1, hex.EncodeToString(verificationHashes[m]))
			}
		}
	}
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

func TestMerkleFileExport(t *testing.T) {
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

	treeData := tree.Export()

	tree2 := ImportMerkleTree(treeData)
	if tree2 == nil {
		fmt.Printf("Error importing tree\n")
		return
	}

	printMerkleTree(tree2)

	// verify both trees
	if tree.fileSize != tree2.fileSize || tree.fragmentSize != tree2.fragmentSize || tree.fragmentCount != tree2.fragmentCount {
		fmt.Printf("Error: Header of trees mismatch\n")
		return
	} else if !bytes.Equal(tree.rootHash, tree2.rootHash) {
		fmt.Printf("Error: Merkle root hash mismatch\n")
		return
	} else if len(tree.fragmentHashes) != len(tree2.fragmentHashes) {
		fmt.Printf("Error: Fragment hashes mismatch count\n")
		return
	} else if len(tree.middleHashes) != len(tree2.middleHashes) {
		fmt.Printf("Error: Middle hashes level mismatch\n")
		return
	}

	// fragment hashes and middle hashes
	for n, hash := range tree.fragmentHashes {
		if !bytes.Equal(hash, tree2.fragmentHashes[n]) {
			fmt.Printf("Error: Fragment hash %d mismatch\n", n)
			return
		}
	}

	for n := range tree.middleHashes {
		if len(tree.middleHashes[n]) != len(tree2.middleHashes[n]) {
			fmt.Printf("Error: Middle hashes level %d mismatch\n", n)
			return
		}

		for m, hash := range tree.middleHashes[n] {
			if !bytes.Equal(hash, tree2.middleHashes[n][m]) {
				fmt.Printf("Error: Middle hash %d %d mismatch\n", n, m)
				return
			}
		}
	}

	fmt.Printf("Success. Import/export match.\n")
}
