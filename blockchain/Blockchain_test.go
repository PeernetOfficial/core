/*
File Name:  Blockchain_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package blockchain

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	mathrand "math/rand"
	"testing"
)

// TestGetBlockRawVersion checks that a block is returned only if the blockchain is of the version asked for.
func TestGetBlockRawVersion(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	height := uint64(2 + mathrand.Intn(5))
	t.Logf("blockchain of %d blocks", height)

	chain := testChain(t, privateKey, int(height), 10)

	// expect checks which blocks of the version are returned: the blocks 0 to height-1 if it is the version of the blockchain, otherwise none.
	expect := func(name string, version uint64, isVersion bool) {
		t.Helper()

		for n := uint64(0); n <= height; n++ {
			raw, status, err := chain.GetBlockRawVersion(version, n)

			if isVersion && n < height {
				if err != nil || status != StatusOK || !bytes.Equal(raw, testChainBlock(t, chain, n)) {
					t.Errorf("%s: block %d of version %d returned status %d (%v), or it differs from the block", name, n, version, status, err)
				}
			} else if err == nil || status != StatusBlockNotFound || raw != nil {
				t.Errorf("%s: block %d of version %d returned status %d (%v), expected that it is not found", name, n, version, status, err)
			}
		}
	}

	expect("version 0", 0, true)
	expect("version 0, asked for version 1", 1, false)

	// The blockchain is deleted and gets blocks of version 1 with the same numbers.
	if status, err := chain.DeleteBlockchain(); status != StatusOK || err != nil {
		t.Fatalf("deleting the blockchain: status %d (%v)", status, err)
	}
	testChainAppend(t, chain, int(height), 10)

	expect("version 1, asked for version 0", 0, false)
	expect("version 1", 1, true)
}
