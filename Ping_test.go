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
	"uuid"

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

// testPeerFarBucket adds a peer to the peer list whose Peer ID falls into the bucket of the farthest peers. It has one active connection to a socket on the loopback address.
func testPeerFarBucket(t *testing.T, backend *Backend, network *Network) (peer *PeerInfo, privateKey ed25519.PrivateKey) {
	for {
		publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
		if publicKey[0]&0x80 == backend.peerPublicKey[0]&0x80 {
			continue
		}

		_, address := testSocket(t)
		peer, _ = backend.PeerlistAdd(publicKey, &Connection{backend: backend, Network: network, Address: address, Status: ConnectionActive, LastPacketIn: time.Now()})
		peer.blockchainLastRefresh = time.Now()

		return peer, privateKey
	}
}

// TestPingAllActivePassive checks active and passive peers. A peer whose bucket is full is in the peer list as a passive peer and is not pinged.
// It is an active peer and is pinged while a transfer with it is running. It enters the routing table with its next packet once the bucket has a free place.
func TestPingAllActivePassive(t *testing.T) {
	backend, network := testBackendLoopback(t)
	pings := testWatchPing(backend)

	inRoutingTable := func(peer *PeerInfo) bool {
		return backend.nodesDHT.IsNodeContact(peer.PublicKey) != nil
	}

	// No packet arrived for longer than the ping time.
	pingDue := func(peers ...*PeerInfo) {
		for _, peer := range peers {
			peer.connectionActive[0].LastPacketIn = time.Now().Add(-(pingTime + 1) * time.Second)
			peer.connectionActive[0].LastPingOut = time.Time{}
		}
	}

	// 20 peers fill the bucket, the next peer for the bucket is not taken into the routing table.
	var peers []*PeerInfo
	for n := 0; n < bucketSize; n++ {
		peer, _ := testPeerFarBucket(t, backend, network)
		peers = append(peers, peer)
	}
	peerPassive, privateKeyPassive := testPeerFarBucket(t, backend, network)

	for n, peer := range peers {
		if !peer.IsActive() || !inRoutingTable(peer) {
			t.Fatalf("peer %d: active %t, in routing table %t", n, peer.IsActive(), inRoutingTable(peer))
		}
	}
	if backend.PeerlistLookup(peerPassive.PublicKey) != peerPassive || peerPassive.IsActive() || inRoutingTable(peerPassive) {
		t.Fatalf("peer 21: in peer list %t, active %t, in routing table %t", backend.PeerlistLookup(peerPassive.PublicKey) == peerPassive, peerPassive.IsActive(), inRoutingTable(peerPassive))
	}

	// Active peers are pinged, the passive peer is not.
	pingDue(append(peers, peerPassive)...)
	backend.pingAll()

	for n, peer := range peers {
		if pings[peer] != 1 {
			t.Fatalf("peer %d: %d pings, expected 1", n, pings[peer])
		}
	}
	if pings[peerPassive] != 0 {
		t.Fatalf("passive peer: %d pings, expected 0", pings[peerPassive])
	}

	// While a transfer runs, the passive peer is an active one and is pinged. It is not in the routing table.
	transfer := newVirtualPacketConn(peerPassive, func(data []byte, sequenceNumber uint32, transferID uuid.UUID) {})

	if !peerPassive.IsActive() || inRoutingTable(peerPassive) {
		t.Fatalf("peer with transfer: active %t, in routing table %t", peerPassive.IsActive(), inRoutingTable(peerPassive))
	}

	backend.pingAll()

	if pings[peerPassive] != 1 {
		t.Fatalf("peer with transfer: %d pings, expected 1", pings[peerPassive])
	}

	transfer.Terminate(2)
	transfer.Terminate(2)

	if peerPassive.IsActive() {
		t.Fatalf("peer after transfer: active")
	}

	// A packet of the passive peer arrives while the bucket is full.
	packet := networkWire{network: network, sender: peerPassive.connectionActive[0].Address, receiverKeyPair: backend.peerKeyPair, raw: testPacket(t, privateKeyPassive, backend.peerPublicKey, protocol.CommandPing, nil), unicast: true}
	testPacketWorker(backend, packet)

	if peerPassive.IsActive() || inRoutingTable(peerPassive) {
		t.Fatalf("passive peer after its packet, bucket full: active %t, in routing table %t", peerPassive.IsActive(), inRoutingTable(peerPassive))
	}

	// One of the 20 peers is removed from the peer list. The passive peer enters the routing table with its next packet.
	connection := peers[5].connectionActive[0]
	peers[5].invalidateActiveConnection(connection)
	connection.Expires = time.Now().Add(-time.Second)
	backend.pingAll()

	if backend.PeerlistLookup(peers[5].PublicKey) != nil || inRoutingTable(peers[5]) {
		t.Fatalf("removed peer: in peer list %t, in routing table %t", backend.PeerlistLookup(peers[5].PublicKey) != nil, inRoutingTable(peers[5]))
	} else if inRoutingTable(peerPassive) {
		t.Fatalf("passive peer: in routing table before its next packet")
	}

	// A packet of a peer that was removed from the peer list does not bring that peer into the routing table.
	backend.routingTableAdd(peers[5])

	if inRoutingTable(peers[5]) {
		t.Fatalf("removed peer: in routing table")
	}

	testPacketWorker(backend, packet)

	if !peerPassive.IsActive() || !inRoutingTable(peerPassive) {
		t.Fatalf("passive peer after its packet, bucket with a free place: active %t, in routing table %t", peerPassive.IsActive(), inRoutingTable(peerPassive))
	} else if backend.nodesDHT.NumNodes() != bucketSize {
		t.Fatalf("%d peers in the routing table, expected %d", backend.nodesDHT.NumNodes(), bucketSize)
	}
}
