/*
File Name:  Ping_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"github.com/PeernetOfficial/core/protocol"
)

// testWatchPing counts the ping messages the backend sends per peer.
func testWatchPing(backend *Backend) (pings map[*PeerInfo]int) {
	pings = make(map[*PeerInfo]int)
	backend.Filters.MessageOutPing = func(peer *PeerInfo, packet *protocol.PacketRaw, connection *Connection) {
		pings[peer]++
	}

	return pings
}

// testPeerLoopback adds a peer to the peer list. It has the given count of active connections, each to another loopback address.
// A packet was just received on every connection, and the blockchain info of the peer was just refreshed.
func testPeerLoopback(t *testing.T, backend *Backend, network *Network, connections int) (peer *PeerInfo) {
	publicKey, _, _ := ed25519.GenerateKey(rand.Reader)

	for n := 0; n < connections; n++ {
		_, address := testSocket(t)
		address = &net.UDPAddr{IP: net.IPv4(127, 0, 0, byte(1+n)), Port: address.Port}
		connection := &Connection{backend: backend, Network: network, Address: address, Status: ConnectionActive, LastPacketIn: time.Now()}

		if n == 0 {
			peer, _ = backend.PeerlistAdd(publicKey, connection)
			peer.blockchainLastRefresh = time.Now()
		} else {
			peer.registerConnection(connection)
		}
	}

	return peer
}

// TestPingAllRemovePeer checks when a peer is removed from the peer list: a connection without incoming packets becomes inactive and is pinged,
// an expired inactive connection is removed, and a peer without a connection is removed from the peer list and from the routing table.
func TestPingAllRemovePeer(t *testing.T) {
	backend, network := testBackendLoopback(t)
	pings := testWatchPing(backend)

	peerSilent := testPeerLoopback(t, backend, network, 1) // no packet arrives any more
	peerLive := testPeerLoopback(t, backend, network, 1)   // packets keep arriving
	peerTwo := testPeerLoopback(t, backend, network, 2)    // two connections, no packet arrives any more on the first one

	inList := func(peer *PeerInfo) bool {
		return backend.PeerlistLookup(peer.PublicKey) == peer
	}
	inRoutingTable := func(peer *PeerInfo) bool {
		return backend.nodesDHT.IsNodeContact(peer.PublicKey) != nil
	}

	// No packet arrived for longer than the time to invalidate a connection.
	silent := time.Now().Add(-(connectionInvalidate + 1) * time.Second)
	connectionSilent := peerSilent.connectionActive[0]
	connectionSilent.LastPacketIn = silent
	connectionTwo := peerTwo.connectionActive[0]
	connectionTwo.LastPacketIn = silent.Add(-connectionInvalidate * time.Second * 4) // the first connection of two is a redundant one

	backend.pingAll()

	if connectionSilent.Status != ConnectionInactive || len(peerSilent.connectionActive) != 0 || len(peerSilent.connectionInactive) != 1 {
		t.Fatalf("silent peer: connection status %d, %d active and %d inactive connections", connectionSilent.Status, len(peerSilent.connectionActive), len(peerSilent.connectionInactive))
	} else if !inList(peerSilent) || !inRoutingTable(peerSilent) {
		t.Fatalf("silent peer: removed while its inactive connection is not expired")
	} else if pings[peerSilent] != 1 {
		t.Fatalf("silent peer: %d pings sent via the inactive connection, expected 1", pings[peerSilent])
	}
	if connectionTwo.Status != ConnectionInactive || len(peerTwo.connectionActive) != 1 || len(peerTwo.connectionInactive) != 1 {
		t.Fatalf("peer with two connections: connection status %d, %d active and %d inactive connections", connectionTwo.Status, len(peerTwo.connectionActive), len(peerTwo.connectionInactive))
	}
	if len(peerLive.connectionActive) != 1 || pings[peerLive] != 0 {
		t.Fatalf("live peer: %d active connections, %d pings", len(peerLive.connectionActive), pings[peerLive])
	}

	// The inactive connections expire.
	connectionSilent.Expires = time.Now().Add(-time.Second)
	connectionTwo.Expires = time.Now().Add(-time.Second)

	backend.pingAll()

	if connectionSilent.Status != ConnectionRemoved || inList(peerSilent) || inRoutingTable(peerSilent) {
		t.Fatalf("silent peer: connection status %d, in peer list %t, in routing table %t", connectionSilent.Status, inList(peerSilent), inRoutingTable(peerSilent))
	}
	if connectionTwo.Status != ConnectionRemoved || len(peerTwo.connectionActive) != 1 || len(peerTwo.connectionInactive) != 0 || !inList(peerTwo) || !inRoutingTable(peerTwo) {
		t.Fatalf("peer with two connections: connection status %d, %d active and %d inactive connections, in peer list %t, in routing table %t", connectionTwo.Status, len(peerTwo.connectionActive), len(peerTwo.connectionInactive), inList(peerTwo), inRoutingTable(peerTwo))
	}
	if !inList(peerLive) || !inRoutingTable(peerLive) {
		t.Fatalf("live peer: removed")
	}

	// A further pass sends nothing to the removed peer.
	backend.pingAll()

	if pings[peerSilent] != 1 {
		t.Fatalf("silent peer: %d pings sent, expected 1", pings[peerSilent])
	} else if backend.PeerlistCount() != 2 {
		t.Fatalf("%d peers in the peer list, expected 2", backend.PeerlistCount())
	}
}
