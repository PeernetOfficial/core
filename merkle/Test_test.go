package merkle

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
	for n := uint64(0); n < tree.FragmentCount; n++ {
		verificationHashes := tree.CreateVerification(n)

		dataSize := tree.FragmentSize
		if n == tree.FragmentCount-1 {
			dataSize = tree.FileSize - n*tree.FragmentSize
		}
		dataHash := blake3.Sum256(data[n*tree.FragmentSize : n*tree.FragmentSize+dataSize])

		valid := MerkleVerify(tree.RootHash, dataHash[:], verificationHashes)

		fmt.Printf("Validate fragment %d: %t\n", n, valid)
		if !valid {
			for m := 0; m < len(verificationHashes); m++ {
				fmt.Printf("-> Middle hash [level %d]: %s\n", m-1, hex.EncodeToString(verificationHashes[m]))
			}
		}
	}
}

func printMerkleTree(tree *MerkleTree) {
	fmt.Printf("File size: %d\n", tree.FileSize)
	fmt.Printf("Fragment size: %d\n", tree.FragmentSize)
	fmt.Printf("Fragment count: %d\n", tree.FragmentCount)

	fmt.Printf("Merkle root hash: %s\n", hex.EncodeToString(tree.RootHash))

	for n := 0; n < len(tree.FragmentHashes); n++ {
		fmt.Printf("Fragment %d: %s\n", n, hex.EncodeToString(tree.FragmentHashes[n]))
	}
	for n := 0; n < len(tree.MiddleHashes); n++ {
		for m := 0; m < len(tree.MiddleHashes[n]); m++ {
			fmt.Printf("Middle hash [level %d] %d: %s\n", n, m, hex.EncodeToString(tree.MiddleHashes[n][m]))
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
	if tree.FileSize != tree2.FileSize || tree.FragmentSize != tree2.FragmentSize || tree.FragmentCount != tree2.FragmentCount {
		fmt.Printf("Error: Header of trees mismatch\n")
		return
	} else if !bytes.Equal(tree.RootHash, tree2.RootHash) {
		fmt.Printf("Error: Merkle root hash mismatch\n")
		return
	} else if len(tree.FragmentHashes) != len(tree2.FragmentHashes) {
		fmt.Printf("Error: Fragment hashes mismatch count\n")
		return
	} else if len(tree.MiddleHashes) != len(tree2.MiddleHashes) {
		fmt.Printf("Error: Middle hashes level mismatch\n")
		return
	}

	// fragment hashes and middle hashes
	for n, hash := range tree.FragmentHashes {
		if !bytes.Equal(hash, tree2.FragmentHashes[n]) {
			fmt.Printf("Error: Fragment hash %d mismatch\n", n)
			return
		}
	}

	for n := range tree.MiddleHashes {
		if len(tree.MiddleHashes[n]) != len(tree2.MiddleHashes[n]) {
			fmt.Printf("Error: Middle hashes level %d mismatch\n", n)
			return
		}

		for m, hash := range tree.MiddleHashes[n] {
			if !bytes.Equal(hash, tree2.MiddleHashes[n][m]) {
				fmt.Printf("Error: Middle hash %d %d mismatch\n", n, m)
				return
			}
		}
	}

	fmt.Printf("Success. Import/export match.\n")
}
