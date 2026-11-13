/*
File Name:  Connection_test.go
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

	"github.com/PeernetOfficial/core/blockchain"
	"github.com/PeernetOfficial/core/dht"
	"github.com/PeernetOfficial/core/protocol"
)

// TestGetConnection2Share checks which connection of a peer is shared: a local one only if allowed, and then preferred. A link-local or loopback one is not shared.
func TestGetConnection2Share(t *testing.T) {
	newConnection := func(ip string) *Connection {
		return &Connection{Address: &net.UDPAddr{IP: net.ParseIP(ip), Port: 112}, PortInternal: 112}
	}

	public4 := newConnection("203.0.113.7")
	local4 := newConnection("192.168.1.7")
	public6 := newConnection("2001:db8::7")
	local6 := newConnection("fd00::7")

	// The public IPv4 connection is the latest one.
	peer := &PeerInfo{connectionActive: []*Connection{public4, local4, public6, local6}, connectionLatest: public4}

	tests := []struct {
		allowLocal, allowIPv4, allowIPv6 bool
		expected                         *Connection
	}{
		{false, true, false, public4},
		{true, true, false, local4},
		{false, false, true, public6},
		{true, false, true, local6},
		{false, false, false, nil},
	}

	for _, test := range tests {
		if result := peer.GetConnection2Share(test.allowLocal, test.allowIPv4, test.allowIPv6); result != test.expected {
			t.Errorf("allow local %t IPv4 %t IPv6 %t: unexpected connection %v", test.allowLocal, test.allowIPv4, test.allowIPv6, result)
		}
	}

	// A peer with public connections only: those are shared also if local ones are allowed.
	peer = &PeerInfo{connectionActive: []*Connection{public4, public6}, connectionLatest: public6}

	if result := peer.GetConnection2Share(true, true, false); result != public4 {
		t.Errorf("public only, IPv4: unexpected connection %v", result)
	}
	if result := peer.GetConnection2Share(true, false, true); result != public6 {
		t.Errorf("public only, IPv6: unexpected connection %v", result)
	}

	// A peer with local connections only: nothing is shared if local ones are not allowed.
	peer = &PeerInfo{connectionActive: []*Connection{local4, local6}, connectionLatest: local4}

	if result := peer.GetConnection2Share(false, true, true); result != nil {
		t.Errorf("local only, local not allowed: unexpected connection %v", result)
	}
	if result := peer.GetConnection2Share(true, true, false); result != local4 {
		t.Errorf("local only, IPv4: unexpected connection %v", result)
	}

	// A peer known only by link-local and loopback addresses is not shared, over a local connection too.
	peer = &PeerInfo{connectionActive: []*Connection{newConnection("169.254.1.7"), newConnection("127.0.0.1"), newConnection("fe80::7"), newConnection("::1")}}
	peer.connectionLatest = peer.connectionActive[0]

	for _, allowLocal := range []bool{false, true} {
		if result := peer.GetConnection2Share(allowLocal, true, true); result != nil {
			t.Errorf("link-local and loopback only, allow local %t: unexpected connection %v", allowLocal, result)
		}
		if peer.IsConnectable(allowLocal, true, true) {
			t.Errorf("link-local and loopback only, allow local %t: peer is connectable", allowLocal)
		}
		if record := peer.peer2Record(allowLocal, true, true, 3); record != nil {
			t.Errorf("link-local and loopback only, allow local %t: unexpected peer record %v", allowLocal, record)
		}
	}

	// Link-local and loopback connections are skipped, also as the latest connection.
	linkLocal4 := newConnection("169.254.1.7")
	peer = &PeerInfo{connectionActive: []*Connection{linkLocal4, newConnection("::1"), local4, public4, local6}, connectionLatest: linkLocal4}

	if result := peer.GetConnection2Share(true, true, false); result != local4 {
		t.Errorf("link-local latest, allow local: unexpected connection %v", result)
	}
	if result := peer.GetConnection2Share(false, true, false); result != public4 {
		t.Errorf("link-local latest: unexpected connection %v", result)
	}
	if result := peer.GetConnection2Share(true, false, true); result != local6 {
		t.Errorf("loopback IPv6, allow local: unexpected connection %v", result)
	}
}

// TestGetConnections2Share checks the order and the count of the connections shared.
func TestGetConnections2Share(t *testing.T) {
	newConnection := func(ip string) *Connection {
		return &Connection{Address: &net.UDPAddr{IP: net.ParseIP(ip), Port: 112}, PortInternal: 112}
	}

	public4a := newConnection("203.0.113.7")
	public4b := newConnection("203.0.113.8")
	public4c := newConnection("203.0.113.9")
	local4a := newConnection("192.168.1.7")
	local4b := newConnection("10.0.0.7")
	public6 := newConnection("2001:db8::7")
	sameAddress := newConnection("203.0.113.8") // the address of public4b, connected via another network
	noInternalPort := &Connection{Address: &net.UDPAddr{IP: net.ParseIP("203.0.113.10"), Port: 112}}

	// public4b is the latest connection.
	peer := &PeerInfo{connectionActive: []*Connection{newConnection("127.0.0.1"), noInternalPort, public4a, local4a, public4b, sameAddress, public6, local4b, public4c, newConnection("169.254.1.7")}, connectionLatest: public4b}

	tests := []struct {
		allowLocal, allowIPv4, allowIPv6 bool
		limit                            int
		expected                         []*Connection
	}{
		{false, true, false, 3, []*Connection{public4b, public4a, public4c}},
		{false, true, false, 2, []*Connection{public4b, public4a}},
		{false, true, false, 1, []*Connection{public4b}},
		{true, true, false, 3, []*Connection{local4a, local4b, public4b}},
		{true, true, false, 1, []*Connection{local4a}},
		{true, true, false, 10, []*Connection{local4a, local4b, public4b, public4a, public4c}},
		{false, false, true, 3, []*Connection{public6}},
		{true, false, true, 3, []*Connection{public6}},
		{false, true, true, 10, []*Connection{public4b, public4a, public6, public4c}},
		{false, false, false, 3, nil},
		{false, true, false, 0, nil},
	}

	for _, test := range tests {
		result := peer.GetConnections2Share(test.allowLocal, test.allowIPv4, test.allowIPv6, test.limit)

		if len(result) != len(test.expected) {
			t.Errorf("allow local %t IPv4 %t IPv6 %t limit %d: %d connections, expected %d", test.allowLocal, test.allowIPv4, test.allowIPv6, test.limit, len(result), len(test.expected))
			continue
		}
		for n := range result {
			if result[n] != test.expected[n] {
				t.Errorf("allow local %t IPv4 %t IPv6 %t limit %d: connection %d is %s, expected %s", test.allowLocal, test.allowIPv4, test.allowIPv6, test.limit, n, result[n].Address.IP.String(), test.expected[n].Address.IP.String())
			}
		}
	}
}

// testSocket returns a UDP socket listening on the loopback address. It is closed when the test ends.
func testSocket(t *testing.T) (socket *net.UDPConn, address *net.UDPAddr) {
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { socket.Close() })

	return socket, socket.LocalAddr().(*net.UDPAddr)
}

// testBackendLoopback returns a backend with an empty peer list that sends via one network on the loopback address.
func testBackendLoopback(t *testing.T) (backend *Backend, network *Network) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	backend = &Backend{peerPublicKey: publicKey, peerPrivateKey: privateKey, Config: &Config{}, UserBlockchain: &blockchain.Blockchain{}, peerList: make(map[[ed25519.PublicKeySize]byte]*PeerInfo)}
	backend.initFilters()
	backend.nodesDHT = dht.NewDHT(&dht.Node{ID: publicKey}, 256, bucketSize, alpha)
	backend.networks = &Networks{backend: backend, Sequences: protocol.NewSequenceManager(ReplyTimeout)}

	socket, address := testSocket(t)
	network = &Network{address: address, socket: socket, backend: backend, networkGroup: backend.networks}
	backend.networks.networks4 = []*Network{network}

	return backend, network
}

// testRootPeers replaces the root peer list with an empty one for the duration of the test.
func testRootPeers(t *testing.T) {
	previous := rootPeers
	rootPeers = make(map[[ed25519.PublicKeySize]byte]*rootPeer)
	t.Cleanup(func() { rootPeers = previous })
}

// testTraverseOut records the Traverse messages a backend sends.
type testTraverseOut struct {
	count       int               // Count of Traverse messages sent.
	relay       *PeerInfo         // Peer the last one was sent to.
	receiverEnd ed25519.PublicKey // End receiver of the last one.
}

// testWatchTraverse records the Traverse messages the backend sends.
func testWatchTraverse(backend *Backend) (traverse *testTraverseOut) {
	traverse = &testTraverseOut{}
	backend.Filters.MessageOutTraverse = func(peer *PeerInfo, packet *protocol.PacketRaw, embeddedPacket *protocol.PacketRaw, receiverEnd ed25519.PublicKey) {
		traverse.count++
		traverse.relay = peer
		traverse.receiverEnd = receiverEnd
	}

	return traverse
}

// check compares the Traverse messages sent since the last check with the expected count. A Traverse message must be sent to the relay for the end receiver.
func (traverse *testTraverseOut) check(t *testing.T, name string, expectedCount int, relay *PeerInfo, receiverEnd ed25519.PublicKey) {
	if traverse.count != expectedCount {
		t.Errorf("%s: %d Traverse messages sent, expected %d", name, traverse.count, expectedCount)
	} else if traverse.count > 0 && (traverse.relay != relay || !traverse.receiverEnd.Equal(receiverEnd)) {
		t.Errorf("%s: Traverse message sent to an unexpected relay or for an unexpected end receiver", name)
	}

	traverse.count = 0
}

// testExpectAnnouncement checks that an Announcement of the backend, sent directly to the peer with the public key, arrived at the socket of that peer.
// It is called after the Announcement was sent. The deadline only ends the test if it is missing.
func testExpectAnnouncement(t *testing.T, name string, backend *Backend, socket *net.UDPConn, publicKey ed25519.PublicKey) {
	buffer := make([]byte, maxPacketSize)
	socket.SetReadDeadline(time.Now().Add(2 * time.Second))
	length, _, err := socket.ReadFromUDP(buffer)
	if err != nil {
		t.Errorf("%s: no direct Announcement received: %v", name, err)
	} else if packet, sender, err := protocol.PacketDecrypt(buffer[:length], publicKey); err != nil || packet.Command != protocol.CommandAnnouncement || !sender.Equal(backend.peerPublicKey) {
		t.Errorf("%s: direct packet is not an Announcement of this peer: %v", name, err)
	}
}

// TestSendTraverseFirstContact checks the first Announcement to a peer that is not in the peer list.
// It is always sent directly. If the peer was reported by another peer, it is additionally sent as Traverse message via the reporting peer,
// whether or not the internal port of the reported peer differs from the observed one, except if the reported peer is a root peer.
// All addresses are loopback addresses: the reported address is a local one and the reporting peer is connected via a local connection.
func TestSendTraverseFirstContact(t *testing.T) {
	backend, network := testBackendLoopback(t)
	testRootPeers(t)
	traverse := testWatchTraverse(backend)

	// The reporting peer is in the peer list.
	reportingPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	_, addressReporting := testSocket(t)
	reporting, _ := backend.PeerlistAdd(reportingPublicKey, &Connection{backend: backend, Network: network, Address: addressReporting, PortInternal: uint16(addressReporting.Port)})

	tests := []struct {
		name          string
		isRootPeer    bool
		portDiffers   bool
		reporting     *PeerInfo
		expectedCount int
	}{
		{"root peer, internal port equal", true, false, reporting, 0},
		{"root peer, internal port differs", true, true, reporting, 0},
		{"peer, internal port differs", false, true, reporting, 1},
		{"peer, internal port equal", false, false, reporting, 1},
		{"peer, no reporting peer", false, false, nil, 0},
	}

	for _, test := range tests {
		targetPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)
		socketTarget, addressTarget := testSocket(t)

		if test.isRootPeer {
			rootPeers[publicKey2Array(targetPublicKey)] = &rootPeer{publicKey: targetPublicKey, backend: backend}
		}

		portInternal := uint16(addressTarget.Port)
		if test.portDiffers {
			portInternal = defaultPort // the socket listens on a port assigned by the system, which is never this one
		}

		backend.contactArbitraryPeer(targetPublicKey, addressTarget, portInternal, test.reporting)

		traverse.check(t, test.name, test.expectedCount, reporting, targetPublicKey)
		testExpectAnnouncement(t, test.name, backend, socketTarget, targetPublicKey)
	}
}

// TestSendTraverseVirtualPeer checks an Announcement to a virtual peer, which is a peer that is not in the peer list.
// It is always sent directly. A virtual peer created for a peer record of a Response has the reporting peer set: the Announcement is additionally sent as Traverse message via it.
// A virtual peer created for the sender of a received Traverse message has none: no Traverse message is sent.
// The virtual peers are created here since records2Nodes and traverseAddresses do not use loopback addresses.
func TestSendTraverseVirtualPeer(t *testing.T) {
	backend, network := testBackendLoopback(t)
	testRootPeers(t)
	traverse := testWatchTraverse(backend)

	// The reporting peer is in the peer list.
	reportingPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	_, addressReporting := testSocket(t)
	reporting, _ := backend.PeerlistAdd(reportingPublicKey, &Connection{backend: backend, Network: network, Address: addressReporting, PortInternal: uint16(addressReporting.Port)})

	tests := []struct {
		name          string
		traversePeer  *PeerInfo
		expectedCount int
	}{
		{"peer record of a Response", reporting, 1},
		{"sender of a received Traverse message", nil, 0},
	}

	for _, test := range tests {
		targetPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)
		socketTarget, addressTarget := testSocket(t)

		// The internal port equals the observed one.
		addresses := []*peerAddress{{IP: addressTarget.IP, Port: uint16(addressTarget.Port), PortInternal: uint16(addressTarget.Port)}}
		virtual := &PeerInfo{Backend: backend, PublicKey: targetPublicKey, isVirtual: true, targetAddresses: addresses, traversePeer: test.traversePeer}

		virtual.sendAnnouncement(true, &protocol.Requests{FindPeer: [][]byte{targetPublicKey}}, nil)

		traverse.check(t, test.name, test.expectedCount, reporting, targetPublicKey)
		testExpectAnnouncement(t, test.name, backend, socketTarget, targetPublicKey)
	}
}

// TestSendTraverseKnownPeer checks the first Announcement to a peer that is in the peer list: it is sent directly only.
func TestSendTraverseKnownPeer(t *testing.T) {
	backend, network := testBackendLoopback(t)
	testRootPeers(t)
	traverse := testWatchTraverse(backend)

	targetPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	socketTarget, addressTarget := testSocket(t)
	target, _ := backend.PeerlistAdd(targetPublicKey, &Connection{backend: backend, Network: network, Address: addressTarget, PortInternal: uint16(addressTarget.Port)})

	target.sendAnnouncement(true, &protocol.Requests{FindSelf: true}, &bootstrapFindSelf{})

	traverse.check(t, "peer in the peer list", 0, nil, targetPublicKey)
	testExpectAnnouncement(t, "peer in the peer list", backend, socketTarget, targetPublicKey)
}
