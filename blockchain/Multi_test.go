/*
File Name:  Multi_test.go
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

	"github.com/PeernetOfficial/core/protocol"
	"github.com/PeernetOfficial/core/store"
)

// testChain returns a new blockchain with the count of blocks, held in memory. Each block has a record of the given size with random data.
func testChain(t *testing.T, privateKey ed25519.PrivateKey, height, recordSize int) (chain *Blockchain) {
	chain, err := InitStore(privateKey, store.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}

	testChainAppend(t, chain, height, recordSize)

	return chain
}

// testChainAppend appends the count of blocks to the blockchain.
func testChainAppend(t *testing.T, chain *Blockchain, count, recordSize int) {
	for n := 0; n < count; n++ {
		data := make([]byte, recordSize)
		rand.Read(data)

		if _, _, status := chain.Append([]BlockRecordRaw{{Type: RecordTypeProfile, Data: data}}); status != StatusOK {
			t.Fatalf("appending block: status %d", status)
		}
	}
}

// testChainBlock returns the block of the blockchain.
func testChainBlock(t *testing.T, chain *Blockchain, number uint64) (raw []byte) {
	raw, status, err := chain.GetBlockRaw(number)
	if err != nil || status != StatusOK {
		t.Fatalf("reading block %d: status %d (%v)", number, status, err)
	}

	return raw
}

// testMultiStore returns a multi store held in memory.
func testMultiStore(t *testing.T) (multi *MultiStore) {
	multi, deleted, err := InitMultiStoreDatabase(store.NewMemoryStore())
	if err != nil || len(deleted) != 0 {
		t.Fatalf("new multi store: %d blockchains deleted (%v)", len(deleted), err)
	}

	return multi
}

// TestIngestBlock checks that the multi store takes a block only as the next block of the blockchain.
// The height in the header is the count of blocks stored, and the blocks stored are block 0 to height-1.
func TestIngestBlock(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	_, otherPrivateKey, _ := ed25519.GenerateKey(rand.Reader)

	height := 3 + mathrand.Intn(10)
	t.Logf("blockchain of %d blocks", height)

	chain := testChain(t, privateKey, height, 20)

	// A second blockchain of the same owner, with the same version and other records. A blockchain of another owner. A blockchain with a new version.
	chainSecond := testChain(t, privateKey, height, 20)
	chainOther := testChain(t, otherPrivateKey, height, 20)
	chainVersion := testChain(t, privateKey, height, 20)
	chainVersion.DeleteBlockchain()
	testChainAppend(t, chainVersion, height, 20)

	// A block above the maximum size, as block 0 of a blockchain.
	chainLarge := testChain(t, privateKey, 1, MaxBlockSize)

	multi := testMultiStore(t)
	header := NewBlockchainHeader(publicKey, 0)

	// expect checks the header in memory and the one stored against the count of blocks ingested.
	expect := func(name string, count uint64) {
		t.Helper()

		if header.Height != count {
			t.Fatalf("%s: height %d, expected %d", name, header.Height, count)
		}

		stored, found, err := multi.ReadBlockchainHeader(publicKey)
		if count == 0 && !found && err == nil {
			return
		} else if err != nil || !found || stored.Height != count || stored.Version != header.Version {
			t.Fatalf("%s: stored header found %t (%v), expected height %d", name, found, err, count)
		}
	}

	// reject checks that the block is not ingested.
	reject := func(name string, raw []byte, count uint64) {
		t.Helper()

		if _, err := multi.IngestBlock(header, raw); err == nil {
			t.Fatalf("%s: block ingested", name)
		}
		expect(name, count)
	}

	reject("block above the maximum size", testChainBlock(t, chainLarge, 0), 0)
	reject("block 1 as first block", testChainBlock(t, chain, 1), 0)
	reject("block 0 of another owner", testChainBlock(t, chainOther, 0), 0)
	reject("block 0 of another version", testChainBlock(t, chainVersion, 0), 0)

	for n := uint64(0); n < uint64(height); n++ {
		if n > 0 {
			reject("block before the next one", testChainBlock(t, chain, n-1), n)
			reject("next block of a blockchain with other blocks before it", testChainBlock(t, chainSecond, n), n)
			reject("next block of another owner", testChainBlock(t, chainOther, n), n)
			reject("next block of another version", testChainBlock(t, chainVersion, n), n)
		}
		if n+1 < uint64(height) {
			reject("block after the next one", testChainBlock(t, chain, n+1), n)
		}

		// A block of the owner that contains the hash of the block before it and has another number.
		last := make([]byte, protocol.HashSize)
		if n > 0 {
			last = protocol.HashData(testChainBlock(t, chain, n-1))
		}
		raw, err := encodeBlock(&Block{OwnerPublicKey: publicKey, LastBlockHash: last, Number: n + 1, RecordsRaw: []BlockRecordRaw{{Type: RecordTypeProfile, Data: make([]byte, 20)}}}, privateKey)
		if err != nil {
			t.Fatal(err)
		}
		reject("block with the hash of the block before it and another number", raw, n)

		decoded, err := multi.IngestBlock(header, testChainBlock(t, chain, n))
		if err != nil || decoded == nil || decoded.Number != n {
			t.Fatalf("block %d: not ingested (%v)", n, err)
		}
		expect("next block", n+1)
	}

	for n := uint64(0); n < uint64(height); n++ {
		if raw, found := multi.ReadBlock(publicKey, 0, n); !found || !bytes.Equal(raw, testChainBlock(t, chain, n)) {
			t.Errorf("block %d: stored block found %t, or it differs from the block ingested", n, found)
		}
	}

	// Deleting the blockchain removes the header and all blocks.
	multi.DeleteBlockchain(header)

	if _, found, _ := multi.ReadBlockchainHeader(publicKey); found {
		t.Errorf("header found after the blockchain was deleted")
	}
	for n := uint64(0); n < uint64(height); n++ {
		if _, found := multi.ReadBlock(publicKey, 0, n); found {
			t.Errorf("block %d found after the blockchain was deleted", n)
		}
	}
}

// TestInitMultiStoreFormat checks that a store without the current format is emptied when it is initialized, and that a store with it is kept.
func TestInitMultiStoreFormat(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	chain := testChain(t, privateKey, 2, 20)

	// A store as an earlier version wrote it: a header with a list of block numbers, the blocks, no format record.
	database := store.NewMemoryStore()
	database.Set(publicKey, make([]byte, 56+2*8))
	database.Set(lookupKeyForBlock(publicKey, 0, 0), testChainBlock(t, chain, 0))
	database.Set(lookupKeyForBlock(publicKey, 0, 1), testChainBlock(t, chain, 1))

	multi, deleted, err := InitMultiStoreDatabase(database)
	if err != nil {
		t.Fatal(err)
	} else if len(deleted) != 1 || !deleted[0].Equal(publicKey) {
		t.Fatalf("store without format: %d blockchains deleted, expected the one stored", len(deleted))
	} else if database.Count() != 1 {
		t.Fatalf("store without format: %d records after it was initialized, expected the format record only", database.Count())
	}

	// A store in the current format is kept.
	header := NewBlockchainHeader(publicKey, 0)
	for n := uint64(0); n < 2; n++ {
		if _, err := multi.IngestBlock(header, testChainBlock(t, chain, n)); err != nil {
			t.Fatal(err)
		}
	}

	multi, deleted, err = InitMultiStoreDatabase(database)
	if err != nil || len(deleted) != 0 {
		t.Fatalf("store in the current format: %d blockchains deleted (%v)", len(deleted), err)
	}
	if stored, found, err := multi.ReadBlockchainHeader(publicKey); err != nil || !found || stored.Height != 2 {
		t.Fatalf("store in the current format: header found %t (%v)", found, err)
	}
}
