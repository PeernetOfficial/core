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

// testBlockRequest requests the block range of the blockchain from the peer. It fails the test if the request does not return within the timeout.
func testBlockRequest(t *testing.T, name string, peer *PeerInfo, blockchainPublicKey ed25519.PublicKey, target protocol.BlockRange, timeout time.Duration) (blocks [][]byte, err error) {
	done := make(chan struct{})

	go func() {
		err = peer.BlockDownload(blockchainPublicKey, target.Limit, 32768, []protocol.BlockRange{target}, func(data []byte, targetBlock protocol.BlockRange, blockSize uint64, availability uint8) {
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

	blocks, err := testBlockRequest(t, "blocks 1 to 3", peerB, backendB.peerPublicKey, protocol.BlockRange{Offset: 1, Limit: 3}, 10*time.Second)
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

// TestBlockRequestRefused checks that a block request returns with an error when the peer refuses it:
// the peer has no block, or it is asked for a blockchain other than its own.
func TestBlockRequestRefused(t *testing.T) {
	testRootPeers(t)

	backendA, networkA := testBackendLive(t, 0)
	backendB, networkB := testBackendLive(t, 0)
	peerB := testConnect(backendA, networkA, backendB, networkB)

	otherPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)

	if _, err := testBlockRequest(t, "peer without a block", peerB, backendB.peerPublicKey, protocol.BlockRange{Offset: 0, Limit: 1}, 5*time.Second); err == nil {
		t.Errorf("peer without a block: no error returned")
	}
	if _, err := testBlockRequest(t, "other blockchain", peerB, otherPublicKey, protocol.BlockRange{Offset: 0, Limit: 1}, 5*time.Second); err == nil {
		t.Errorf("other blockchain: no error returned")
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

	if _, err := testBlockRequest(t, "silent peer", peer, peer.PublicKey, protocol.BlockRange{Offset: 0, Limit: 1}, 15*time.Second); err == nil {
		t.Errorf("silent peer: no error returned")
	}
}
