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
	"errors"
	mathrand "math/rand"
	"testing"
	"time"

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

	reasons := map[uint8]MultiReason{3: {Since: time.Unix(1000, 0), Until: time.Unix(2000, 0)}}
	if err := multi.WriteReasons(publicKey, reasons); err != nil {
		t.Fatal(err)
	}

	multi, deleted, err = InitMultiStoreDatabase(database)
	if err != nil || len(deleted) != 0 {
		t.Fatalf("store in the current format: %d blockchains deleted (%v)", len(deleted), err)
	}
	if stored, found, err := multi.ReadBlockchainHeader(publicKey); err != nil || !found || stored.Height != 2 {
		t.Fatalf("store in the current format: header found %t (%v)", found, err)
	}
	if stored, err := multi.ReadReasons(publicKey); err != nil || len(stored) != 1 || stored[3] != reasons[3] {
		t.Fatalf("store in the current format: %d reasons (%v), expected the one stored", len(stored), err)
	}
}

// TestReasons checks that the multi store returns the reasons for which a blockchain is kept as they were written.
func TestReasons(t *testing.T) {
	multi := testMultiStore(t)

	// testIterate returns the public keys of the blockchains that reasons are stored for.
	testIterate := func() (publicKeys []ed25519.PublicKey) {
		multi.IterateReasons(func(publicKey ed25519.PublicKey) {
			publicKeys = append(publicKeys, publicKey)
		})
		return publicKeys
	}

	publicKey1, privateKey1, _ := ed25519.GenerateKey(rand.Reader)
	publicKey2, _, _ := ed25519.GenerateKey(rand.Reader)

	// A blockchain with blocks and without reasons.
	chain := testChain(t, privateKey1, 2, 20)
	header := NewBlockchainHeader(publicKey1, 0)
	for n := uint64(0); n < 2; n++ {
		if _, err := multi.IngestBlock(header, testChainBlock(t, chain, n)); err != nil {
			t.Fatal(err)
		}
	}

	if reasons, err := multi.ReadReasons(publicKey1); err != nil || len(reasons) != 0 {
		t.Fatalf("no reasons written: %d reasons (%v)", len(reasons), err)
	} else if stored := testIterate(); len(stored) != 0 {
		t.Fatalf("no reasons written: reasons stored for %d blockchains", len(stored))
	}

	// Reasons with random numbers and times.
	written := make(map[uint8]MultiReason)
	for n := 0; n < 1+mathrand.Intn(10); n++ {
		written[uint8(mathrand.Intn(256))] = MultiReason{Since: time.Unix(mathrand.Int63n(1<<40), 0), Until: time.Unix(mathrand.Int63n(1<<40), 0)}
	}
	t.Logf("reasons %v", written)

	if err := multi.WriteReasons(publicKey1, written); err != nil {
		t.Fatal(err)
	} else if err := multi.WriteReasons(publicKey2, map[uint8]MultiReason{0: {Since: time.Unix(1, 0), Until: time.Unix(2, 0)}}); err != nil {
		t.Fatal(err)
	}

	reasons, err := multi.ReadReasons(publicKey1)
	if err != nil || len(reasons) != len(written) {
		t.Fatalf("%d reasons read (%v), expected %d", len(reasons), err, len(written))
	}
	for number, reason := range written {
		if !reasons[number].Since.Equal(reason.Since) || !reasons[number].Until.Equal(reason.Until) {
			t.Fatalf("reason %d read as %v, expected %v", number, reasons[number], reason)
		}
	}

	if stored := testIterate(); len(stored) != 2 || !(stored[0].Equal(publicKey1) && stored[1].Equal(publicKey2) || stored[0].Equal(publicKey2) && stored[1].Equal(publicKey1)) {
		t.Fatalf("reasons stored for %d blockchains, expected the 2 written", len(stored))
	}

	// The header and the blocks are stored apart from the reasons.
	if stored, found, err := multi.ReadBlockchainHeader(publicKey1); err != nil || !found || stored.Height != 2 {
		t.Fatalf("header found %t (%v)", found, err)
	}
	multi.DeleteBlockchain(header)
	if reasons, err := multi.ReadReasons(publicKey1); err != nil || len(reasons) != len(written) {
		t.Fatalf("blockchain deleted: %d reasons read (%v), expected %d", len(reasons), err, len(written))
	}

	// Without a reason the record is deleted.
	if err := multi.WriteReasons(publicKey1, nil); err != nil {
		t.Fatal(err)
	} else if stored := testIterate(); len(stored) != 1 || !stored[0].Equal(publicKey2) {
		t.Fatalf("reasons removed: reasons stored for %d blockchains, expected 1", len(stored))
	}

	// A record of another length is not read.
	multi.Database.Set(lookupKeyForReasons(publicKey2), make([]byte, multiReasonSize+1))
	if _, err := multi.ReadReasons(publicKey2); err == nil {
		t.Fatalf("record of an invalid length: no error")
	}
}

// testStoreFail is a store whose writes of blocks fail while fail is set.
type testStoreFail struct {
	store.Store
	fail bool
}

func (database *testStoreFail) Set(key []byte, data []byte) error {
	if database.fail && len(key) == ed25519.PublicKeySize+16 {
		return errors.New("write fails")
	}

	return database.Store.Set(key, data)
}

// TestStoreSize checks the size that the multi store keeps of each blockchain in its header and of all blockchains together:
// the size of the blocks stored, also after the store is initialized again.
func TestStoreSize(t *testing.T) {
	database := &testStoreFail{Store: store.NewMemoryStore()}
	multi, _, err := InitMultiStoreDatabase(database)
	if err != nil {
		t.Fatal(err)
	}

	// expect checks the size of the store, also after it is initialized again.
	expect := func(name string, size uint64) {
		t.Helper()

		if multi.Size() != size {
			t.Fatalf("%s: size of the store %d, expected %d", name, multi.Size(), size)
		}

		if multi, _, err = InitMultiStoreDatabase(database); err != nil {
			t.Fatal(err)
		} else if multi.Size() != size {
			t.Fatalf("%s: size of the store %d after it was initialized again, expected %d", name, multi.Size(), size)
		}
	}

	expect("empty store", 0)

	// Two blockchains with blocks of random sizes.
	var headers [2]*MultiBlockchainHeader
	var sizes [2]uint64

	for n := range headers {
		publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
		height := 2 + mathrand.Intn(8)
		chain := testChain(t, privateKey, 0, 0)
		headers[n] = NewBlockchainHeader(publicKey, 0)

		for block := 0; block < height; block++ {
			testChainAppend(t, chain, 1, 2+mathrand.Intn(5000))
			raw := testChainBlock(t, chain, uint64(block))
			sizes[n] += uint64(len(raw))

			if _, err := multi.IngestBlock(headers[n], raw); err != nil {
				t.Fatal(err)
			}
		}

		if stored, found, err := multi.ReadBlockchainHeader(publicKey); err != nil || !found || stored.Size != sizes[n] || headers[n].Size != sizes[n] {
			t.Fatalf("blockchain %d: header found %t (%v), size %d in memory, expected %d", n, found, err, headers[n].Size, sizes[n])
		}

		// A block that is rejected and a block whose write fails leave the sizes as they are.
		if _, err := multi.IngestBlock(headers[n], testChainBlock(t, chain, 0)); err == nil {
			t.Fatalf("blockchain %d: block 0 ingested a second time", n)
		}

		testChainAppend(t, chain, 1, 100)
		database.fail = true
		_, err := multi.IngestBlock(headers[n], testChainBlock(t, chain, uint64(height)))
		database.fail = false

		if err == nil || headers[n].Size != sizes[n] || headers[n].Height != uint64(height) {
			t.Fatalf("blockchain %d: write of a block fails: error %v, height %d, size %d", n, err, headers[n].Height, headers[n].Size)
		}
	}
	t.Logf("blockchains of %d and %d bytes", sizes[0], sizes[1])

	expect("two blockchains", sizes[0]+sizes[1])

	multi.DeleteBlockchain(headers[0])
	expect("first blockchain deleted", sizes[1])

	multi.DeleteBlockchain(headers[1])
	expect("both blockchains deleted", 0)
}
