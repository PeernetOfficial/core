/*
File Name:  Transfer Block_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	mathrand "math/rand"
	"testing"
	"time"

	"github.com/PeernetOfficial/core/protocol"
)

// testBackendLive returns a backend with one network on the loopback address. It receives and processes packets until the test ends.
// Its blockchain has the given height.
func testBackendLive(t *testing.T, height int) (backend *Backend, network *Network) {
	backend, network = testBackendLoopback(t)
	backend.UserBlockchain = testBlockchain(t, backend.peerPrivateKey, height)
	backend.networks.rawPacketsIncoming = make(chan networkWire, 1000)
	backend.networks.litePacketsIncoming = make(chan networkWire, 1000)
	backend.networks.LiteRouter = protocol.NewLiteRouter()

	t.Cleanup(func() {
		network.isTerminated = true
		network.socket.Close()
	})

	go network.Listen()
	go backend.networks.packetWorker()
	go backend.networks.packetWorkerLite()

	return backend, network
}

// testConnect adds the peer of the other backend to the peer list of the backend.
func testConnect(backend *Backend, network *Network, other *Backend, networkOther *Network) (peer *PeerInfo) {
	peer, _ = backend.PeerlistAdd(other.peerPublicKey, &Connection{backend: backend, Network: network, Address: networkOther.address, PortInternal: uint16(networkOther.address.Port)})
	return peer
}

// testBlockRequest requests the block range of the blockchain in the given version from the peer. It fails the test if the request does not return within the timeout.
func testBlockRequest(t *testing.T, name string, peer *PeerInfo, blockchainPublicKey ed25519.PublicKey, blockchainVersion uint64, target protocol.BlockRange, timeout time.Duration) (blocks [][]byte, err error) {
	done := make(chan struct{})

	go func() {
		err = peer.BlockDownload(blockchainPublicKey, blockchainVersion, target.Limit, 32768, []protocol.BlockRange{target}, func(data []byte, targetBlock protocol.BlockRange, blockSize uint64, availability uint8) {
			if availability == protocol.GetBlockStatusAvailable {
				blocks = append(blocks, data)
			}
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("%s: the block request did not return within %s", name, timeout)
	}

	return blocks, err
}

// TestBlockDownload requests blocks from a peer through the packet workers of two backends on the loopback address.
func TestBlockDownload(t *testing.T) {
	testRootPeers(t)

	backendA, networkA := testBackendLive(t, 0)
	backendB, networkB := testBackendLive(t, 5)
	peerB := testConnect(backendA, networkA, backendB, networkB)

	blocks, err := testBlockRequest(t, "blocks 1 to 3", peerB, backendB.peerPublicKey, 0, protocol.BlockRange{Offset: 1, Limit: 3}, 10*time.Second)
	if err != nil {
		t.Fatalf("blocks 1 to 3: %v", err)
	} else if len(blocks) != 3 {
		t.Fatalf("blocks 1 to 3: received %d blocks, expected 3", len(blocks))
	}

	for n, block := range blocks {
		if expected, _, _ := backendB.UserBlockchain.GetBlockRaw(uint64(1 + n)); !bytes.Equal(block, expected) {
			t.Errorf("block %d differs from the block of the peer", 1+n)
		}
	}
}

// TestBlockDownloadAboveHeight requests a range of blocks that reaches far above the height of the blockchain of the peer.
// The blocks below the height are received, and the request returns.
func TestBlockDownloadAboveHeight(t *testing.T) {
	testRootPeers(t)

	backendA, networkA := testBackendLive(t, 0)
	backendB, networkB := testBackendLive(t, 5)
	peerB := testConnect(backendA, networkA, backendB, networkB)

	blocks, err := testBlockRequest(t, "blocks 3 to 2^40", peerB, backendB.peerPublicKey, 0, protocol.BlockRange{Offset: 3, Limit: 1 << 40}, 10*time.Second)
	if err != nil {
		t.Fatalf("blocks 3 to 2^40: %v", err)
	} else if len(blocks) != 2 {
		t.Fatalf("blocks 3 to 2^40: received %d blocks, expected 2", len(blocks))
	}

	for n, block := range blocks {
		if expected, _, _ := backendB.UserBlockchain.GetBlockRaw(uint64(3 + n)); !bytes.Equal(block, expected) {
			t.Errorf("block %d differs from the block of the peer", 3+n)
		}
	}
}

// testSent is one entry of a block transfer: the header and, if the block is available, the block.
type testSent struct {
	availability uint8
	target       protocol.BlockRange
	blockSize    uint64
	data         []byte
}

// testSendBlocks returns the entries that sendBlocks writes for the request. The blockchain has the given blocks; a block that is nil is not found.
func testSendBlocks(t *testing.T, name string, chain [][]byte, limitBlockCount, maxBlockSize uint64, targets []protocol.BlockRange) (sent []testSent) {
	t.Helper()

	var buffer bytes.Buffer

	err := sendBlocks(&buffer, func(blockNumber uint64) (raw []byte, found bool) {
		return chain[blockNumber], chain[blockNumber] != nil
	}, uint64(len(chain)), limitBlockCount, maxBlockSize, targets)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}

	for buffer.Len() > 0 {
		data, target, blockSize, availability, err := protocol.BlockTransferReadBlock(&buffer, math.MaxUint64)
		if err != nil {
			t.Fatalf("%s: reading entry %d: %v", name, len(sent), err)
		}
		sent = append(sent, testSent{availability: availability, target: target, blockSize: blockSize, data: data})
	}

	return sent
}

// testSentExpect checks the entries of a block transfer against the expected ones.
func testSentExpect(t *testing.T, name string, sent, expected []testSent) {
	t.Helper()

	if len(sent) != len(expected) {
		t.Fatalf("%s: %d entries sent, expected %d", name, len(sent), len(expected))
	}

	for n := range sent {
		if sent[n].availability != expected[n].availability || sent[n].target != expected[n].target || sent[n].blockSize != expected[n].blockSize || !bytes.Equal(sent[n].data, expected[n].data) {
			t.Errorf("%s: entry %d is availability %d, range %d count %d, size %d, expected availability %d, range %d count %d, size %d, or the block differs", name, n,
				sent[n].availability, sent[n].target.Offset, sent[n].target.Limit, sent[n].blockSize, expected[n].availability, expected[n].target.Offset, expected[n].target.Limit, expected[n].blockSize)
		}
	}
}

// TestSendBlocks checks the entries that are sent for a block request: blocks below the height one by one, the part of a range from the height on as one entry,
// and the end at the limit of blocks.
func TestSendBlocks(t *testing.T) {
	// A blockchain of 6 to 11 blocks of 100 to 199 bytes of random data. Block 2 is larger than block 1.
	height := uint64(6 + mathrand.Intn(6))
	chain := make([][]byte, height)
	for n := range chain {
		chain[n] = make([]byte, 100+mathrand.Intn(100))
		if n == 2 {
			chain[n] = make([]byte, len(chain[1])+1+mathrand.Intn(100))
		}
		rand.Read(chain[n])
	}
	t.Logf("blockchain of %d blocks", height)

	available := func(number uint64) testSent {
		return testSent{availability: protocol.GetBlockStatusAvailable, target: protocol.BlockRange{Offset: number, Limit: 1}, blockSize: uint64(len(chain[number])), data: chain[number]}
	}
	notAvailable := func(number, count uint64) testSent {
		return testSent{availability: protocol.GetBlockStatusNotAvailable, target: protocol.BlockRange{Offset: number, Limit: count}}
	}
	ranges := func(numbers ...uint64) (targets []protocol.BlockRange) {
		for n := 0; n < len(numbers); n += 2 {
			targets = append(targets, protocol.BlockRange{Offset: numbers[n], Limit: numbers[n+1]})
		}
		return targets
	}

	const noLimit = math.MaxUint64

	sent := testSendBlocks(t, "below the height", chain, noLimit, noLimit, ranges(1, 3))
	testSentExpect(t, "below the height", sent, []testSent{available(1), available(2), available(3)})

	sent = testSendBlocks(t, "across the height", chain, noLimit, noLimit, ranges(height-2, 1<<40))
	testSentExpect(t, "across the height", sent, []testSent{available(height - 2), available(height - 1), notAvailable(height, 1<<40-2)})

	sent = testSendBlocks(t, "from the height on", chain, noLimit, noLimit, ranges(height, 7))
	testSentExpect(t, "from the height on", sent, []testSent{notAvailable(height, 7)})

	sent = testSendBlocks(t, "above the height", chain, noLimit, noLimit, ranges(height+3, 1<<40, 0, 1))
	testSentExpect(t, "above the height", sent, []testSent{notAvailable(height+3, 1<<40), available(0)})

	// The offset and the count of a range together exceed 64 bits.
	sent = testSendBlocks(t, "count of 2^64-1", chain, noLimit, noLimit, ranges(height-1, math.MaxUint64))
	testSentExpect(t, "count of 2^64-1", sent, []testSent{available(height - 1), notAvailable(height, math.MaxUint64-1)})

	sent = testSendBlocks(t, "offset of 2^64-1", chain, noLimit, noLimit, ranges(math.MaxUint64, 5))
	testSentExpect(t, "offset of 2^64-1", sent, []testSent{notAvailable(math.MaxUint64, 5)})

	sent = testSendBlocks(t, "count of 0", chain, noLimit, noLimit, ranges(2, 0, height+1, 0))
	testSentExpect(t, "count of 0", sent, nil)

	// The limit of blocks ends the transfer, also when further ranges follow.
	sent = testSendBlocks(t, "limit of 1, two ranges", chain, 1, noLimit, ranges(0, 1, 2, 1))
	testSentExpect(t, "limit of 1, two ranges", sent, []testSent{available(0)})

	sent = testSendBlocks(t, "limit of 3, two ranges", chain, 3, noLimit, ranges(0, 2, 4, 2))
	testSentExpect(t, "limit of 3, two ranges", sent, []testSent{available(0), available(1), available(4)})

	sent = testSendBlocks(t, "limit of 2, one range", chain, 2, noLimit, ranges(1, 4))
	testSentExpect(t, "limit of 2, one range", sent, []testSent{available(1), available(2)})

	// A block above the max block size is reported with its size. It does not count as a block sent. Block 1 has the max block size.
	maxBlockSize := uint64(len(chain[1]))
	sizeExceed := testSent{availability: protocol.GetBlockStatusSizeExceed, target: protocol.BlockRange{Offset: 2, Limit: 1}, blockSize: uint64(len(chain[2]))}

	sent = testSendBlocks(t, "max block size", chain, 2, maxBlockSize, ranges(1, 2, 1, 1, 1, 1))
	testSentExpect(t, "max block size", sent, []testSent{available(1), sizeExceed, available(1)})

	var expected []testSent

	// A block below the height that is not found is reported alone. It does not count as a block sent.
	missing := uint64(1 + mathrand.Intn(3))
	t.Logf("block %d is not found", missing)
	block := chain[missing]
	chain[missing] = nil

	expected = nil
	for n := uint64(0); n < 5; n++ {
		if n == missing {
			expected = append(expected, notAvailable(n, 1))
		} else {
			expected = append(expected, available(n))
		}
	}
	sent = testSendBlocks(t, "block not found", chain, 4, noLimit, ranges(0, height))
	testSentExpect(t, "block not found", sent, expected)

	chain[missing] = block
}

// testWriter is a writer of which one write fails: the one after the given count of writes.
type testWriter struct {
	writes int
}

func (writer *testWriter) Write(data []byte) (n int, err error) {
	if writer.writes--; writer.writes == -1 {
		return 0, errors.New("write failed")
	}

	return len(data), nil
}

// TestSendBlocksWriteError checks that a block transfer ends at the first write that fails: no further block is read.
func TestSendBlocksWriteError(t *testing.T) {
	const height = 10

	// Each block is two writes, the header and the block. Every other entry is one write.
	run := func(name string, writes int, found bool, maxBlockSize uint64, targets []protocol.BlockRange, expectRead int) {
		countRead := 0

		err := sendBlocks(&testWriter{writes: writes}, func(blockNumber uint64) (raw []byte, isFound bool) {
			countRead++
			return []byte{1, 2, 3}, found
		}, height, math.MaxUint64, maxBlockSize, targets)

		if err == nil {
			t.Errorf("%s: no error returned", name)
		}
		if countRead != expectRead {
			t.Errorf("%s: %d blocks read, expected %d", name, countRead, expectRead)
		}
	}

	for writes := 0; writes < 2*height; writes++ {
		run(fmt.Sprintf("blocks below the height, %d writes succeed", writes), writes, true, math.MaxUint64, []protocol.BlockRange{{Offset: 0, Limit: height}}, writes/2+1)
	}
	run("blocks from the height on", 0, true, math.MaxUint64, []protocol.BlockRange{{Offset: height, Limit: 5}, {Offset: 0, Limit: height}}, 0)
	run("block not found", 0, false, math.MaxUint64, []protocol.BlockRange{{Offset: 0, Limit: height}}, 1)
	run("block above the max block size", 0, true, 2, []protocol.BlockRange{{Offset: 0, Limit: height}}, 1)
}

// TestBlockRequestRefused checks that a block request returns with an error when the peer refuses it:
// the peer has no block, it is asked for a blockchain other than its own, or for a version of its blockchain other than the one it has.
func TestBlockRequestRefused(t *testing.T) {
	testRootPeers(t)

	backendA, networkA := testBackendLive(t, 0)
	backendB, networkB := testBackendLive(t, 0)
	backendC, networkC := testBackendLive(t, 3)
	peerB := testConnect(backendA, networkA, backendB, networkB)
	peerC := testConnect(backendA, networkA, backendC, networkC)

	otherPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)

	if _, err := testBlockRequest(t, "peer without a block", peerB, backendB.peerPublicKey, 0, protocol.BlockRange{Offset: 0, Limit: 1}, 5*time.Second); err == nil {
		t.Errorf("peer without a block: no error returned")
	}
	if _, err := testBlockRequest(t, "other blockchain", peerB, otherPublicKey, 0, protocol.BlockRange{Offset: 0, Limit: 1}, 5*time.Second); err == nil {
		t.Errorf("other blockchain: no error returned")
	}
	if blocks, err := testBlockRequest(t, "other version", peerC, backendC.peerPublicKey, 1, protocol.BlockRange{Offset: 0, Limit: 1}, 5*time.Second); err == nil {
		t.Errorf("other version: no error returned, %d blocks received", len(blocks))
	}
}

// TestStoredBlocks checks for which blockchain and version a peer has blocks to send: for its own blockchain in the version it has.
// The blocks are read while the blockchain is of that version.
func TestStoredBlocks(t *testing.T) {
	testRootPeers(t)

	height := uint64(3 + mathrand.Intn(5))
	t.Logf("blockchain of %d blocks", height)

	backend, _ := testBackendLive(t, int(height))
	otherPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)

	if _, _, found := backend.storedBlocks(otherPublicKey, 0); found {
		t.Errorf("other blockchain: found")
	}
	if _, _, found := backend.storedBlocks(backend.peerPublicKey, 1); found {
		t.Errorf("version 1 of the own blockchain of version 0: found")
	}

	heightStored, read, found := backend.storedBlocks(backend.peerPublicKey, 0)
	if !found || heightStored != height {
		t.Fatalf("own blockchain: found %t, height %d, expected height %d", found, heightStored, height)
	}

	for n := uint64(0); n < height; n++ {
		expected, _, _ := backend.UserBlockchain.GetBlockRaw(n)
		if raw, found := read(n); !found || !bytes.Equal(raw, expected) {
			t.Errorf("own blockchain: block %d found %t, or it differs from the block", n, found)
		}
	}
	if _, found := read(height); found {
		t.Errorf("own blockchain: block %d found, above the height", height)
	}

	// The blockchain gets a new version with blocks of the same numbers. The blocks of version 0 are no longer read.
	backend.UserBlockchain.DeleteBlockchain()
	testBlockchainAppend(t, backend, int(height)+1)

	for n := uint64(0); n < height; n++ {
		if _, found := read(n); found {
			t.Errorf("new version: block %d of version 0 found", n)
		}
	}

	if _, _, found := backend.storedBlocks(backend.peerPublicKey, 0); found {
		t.Errorf("new version: version 0 found")
	}
	if heightStored, _, found := backend.storedBlocks(backend.peerPublicKey, 1); !found || heightStored != height+1 {
		t.Errorf("new version: found %t, height %d, expected height %d", found, heightStored, height+1)
	}
}

// TestBlockRequestNoAnswer checks that a block request returns with an error when the peer does not answer.
func TestBlockRequestNoAnswer(t *testing.T) {
	testRootPeers(t)

	previous := blockSequenceTimeout
	blockSequenceTimeout = 500 * time.Millisecond
	t.Cleanup(func() { blockSequenceTimeout = previous })

	backend, network := testBackendLive(t, 0)
	peer, _, _ := testPeerSocket(t, backend, network)

	if _, err := testBlockRequest(t, "silent peer", peer, peer.PublicKey, 0, protocol.BlockRange{Offset: 0, Limit: 1}, 15*time.Second); err == nil {
		t.Errorf("silent peer: no error returned")
	}
}
