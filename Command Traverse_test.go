/*
File Name:  Command Traverse_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"

	"github.com/PeernetOfficial/core/protocol"
)

// TestTraverseAddresses checks which addresses of the original sender in a received traverse message are used:
// a local address only if the message was received via a local connection, a link-local or loopback address never.
func TestTraverseAddresses(t *testing.T) {
	tests := []struct {
		ipv4, ipv6 string
		allowLocal bool
		expected   []string
	}{
		{"203.0.113.7", "2001:db8::7", false, []string{"203.0.113.7", "2001:db8::7"}},
		{"203.0.113.7", "2001:db8::7", true, []string{"203.0.113.7", "2001:db8::7"}},
		{"203.0.113.7", "::", false, []string{"203.0.113.7"}},
		{"0.0.0.0", "2001:db8::7", false, []string{"2001:db8::7"}},
		{"0.0.0.0", "::", true, nil},
		{"192.168.1.7", "fd00::7", false, nil},
		{"192.168.1.7", "fd00::7", true, []string{"192.168.1.7", "fd00::7"}},
		{"10.0.0.7", "2001:db8::7", false, []string{"2001:db8::7"}},

		// link-local and loopback addresses
		{"169.254.1.7", "fe80::7", false, nil},
		{"169.254.1.7", "fe80::7", true, nil},
		{"127.0.0.1", "::1", false, nil},
		{"127.0.0.1", "::1", true, nil},
		{"127.0.0.1", "2001:db8::7", true, []string{"2001:db8::7"}},
		{"192.168.1.7", "fe80::7", true, []string{"192.168.1.7"}},
		{"169.254.1.7", "fd00::7", true, []string{"fd00::7"}},

		// carrier-grade NAT addresses
		{"100.64.0.1", "::", false, nil},
		{"100.64.0.1", "::", true, nil},
		{"100.64.0.1", "2001:db8::7", false, []string{"2001:db8::7"}},
		{"100.127.255.255", "fd00::7", true, []string{"fd00::7"}},
	}

	for _, test := range tests {
		msg := &protocol.MessageTraverse{IPv4: net.ParseIP(test.ipv4).To4(), PortIPv4: 1001, IPv6: net.ParseIP(test.ipv6), PortIPv6: 1002}

		addresses := traverseAddresses(msg, test.allowLocal)

		if len(addresses) != len(test.expected) {
			t.Errorf("traverse %s / %s allow local %t: %d addresses, expected %d", test.ipv4, test.ipv6, test.allowLocal, len(addresses), len(test.expected))
			continue
		}
		for n, address := range addresses {
			if !address.IP.Equal(net.ParseIP(test.expected[n])) {
				t.Errorf("traverse %s / %s allow local %t: address %s, expected %s", test.ipv4, test.ipv6, test.allowLocal, address.IP.String(), test.expected[n])
			}
		}
	}

	// The external port is used if reported.
	msg := &protocol.MessageTraverse{IPv4: net.ParseIP("203.0.113.7").To4(), PortIPv4: 1001, PortIPv4ReportedExternal: 2001, IPv6: net.ParseIP("2001:db8::7"), PortIPv6: 1002}
	if addresses := traverseAddresses(msg, false); len(addresses) != 2 || addresses[0].Port != 2001 || addresses[1].Port != 1002 {
		t.Errorf("external port: unexpected addresses %v", addresses)
	}
}

// TestTraverseForwardRootPeer checks that a Traverse message is forwarded to the end receiver, except if the end receiver is a root peer.
func TestTraverseForwardRootPeer(t *testing.T) {
	backend, network := testBackendLoopback(t)
	testRootPeers(t)

	traverseCount := 0
	backend.Filters.PacketOut = func(packet *protocol.PacketRaw, receiverPublicKey ed25519.PublicKey, connection *Connection) {
		if packet.Command == protocol.CommandTraverse {
			traverseCount++
		}
	}

	// The original sender is connected via a public address.
	senderPublicKey, senderPrivateKey, _ := ed25519.GenerateKey(rand.Reader)
	connectionSender := &Connection{Address: &net.UDPAddr{IP: net.ParseIP("203.0.113.7").To4(), Port: 1001}, PortInternal: 112}
	sender := &PeerInfo{Backend: backend, PublicKey: senderPublicKey, connectionActive: []*Connection{connectionSender}, connectionLatest: connectionSender}

	for _, isRootPeer := range []bool{false, true} {
		// The end receiver is in the peer list.
		targetPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)
		_, addressTarget := testSocket(t)

		if isRootPeer {
			rootPeers[publicKey2Array(targetPublicKey)] = &rootPeer{publicKey: targetPublicKey, backend: backend}
		}

		target, _ := backend.PeerlistAdd(targetPublicKey, &Connection{backend: backend, Network: network, Address: addressTarget, PortInternal: uint16(addressTarget.Port)})
		target.Features = 1 << protocol.FeatureIPv4Listen

		// The Traverse message as the original sender creates it.
		announcement := protocol.EncodeAnnouncement(true, &protocol.Requests{FindSelf: true}, 1<<protocol.FeatureIPv4Listen, 0, 0, "test")
		embedded, err := protocol.PacketEncryptEmbedded(senderPrivateKey, targetPublicKey, backend.peerPublicKey, &protocol.PacketRaw{Protocol: protocol.ProtocolVersion, Command: protocol.CommandAnnouncement, Payload: announcement[0]})
		if err != nil {
			t.Fatal(err)
		}
		payload, err := protocol.EncodeTraverse(embedded, targetPublicKey)
		if err != nil {
			t.Fatal(err)
		}
		msg, err := protocol.DecodeTraverse(&protocol.MessageRaw{PacketRaw: protocol.PacketRaw{Command: protocol.CommandTraverse, Payload: payload}, SenderPublicKey: senderPublicKey})
		if err != nil {
			t.Fatal(err)
		}

		traverseCount = 0
		sender.cmdTraverseForward(msg, connectionSender)

		if isRootPeer && traverseCount != 0 {
			t.Errorf("root peer: %d Traverse messages forwarded, expected none", traverseCount)
		} else if !isRootPeer && traverseCount != 1 {
			t.Errorf("peer: %d Traverse messages forwarded, expected 1", traverseCount)
		}
	}
}

// TestTraverseReceiveAnswer checks that the Announcement embedded in a received Traverse message is answered with a Response to the original sender,
// and that this answer is not accompanied by a Traverse message.
func TestTraverseReceiveAnswer(t *testing.T) {
	backend, network := testBackendLoopback(t)
	testRootPeers(t)
	traverse := testWatchTraverse(backend)

	senderPublicKey, senderPrivateKey, _ := ed25519.GenerateKey(rand.Reader)

	responseCount := 0
	backend.Filters.MessageOutResponse = func(peer *PeerInfo, packet *protocol.PacketRaw, records []protocol.ResponseRecord) {
		if peer.PublicKey.Equal(senderPublicKey) {
			responseCount++
		}
	}

	// The relay is in the peer list, connected via a local connection.
	relayPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	_, addressRelay := testSocket(t)
	connectionRelay := &Connection{backend: backend, Network: network, Address: addressRelay, PortInternal: uint16(addressRelay.Port)}
	relay, _ := backend.PeerlistAdd(relayPublicKey, connectionRelay)

	// The Traverse message as the relay forwards it. The address of the original sender is a local one, which is used since the message is received via a local connection.
	announcement := protocol.EncodeAnnouncement(true, &protocol.Requests{FindSelf: true}, 1<<protocol.FeatureIPv4Listen, 0, 0, "test")
	embedded, err := protocol.PacketEncryptEmbedded(senderPrivateKey, backend.peerPublicKey, relayPublicKey, &protocol.PacketRaw{Protocol: protocol.ProtocolVersion, Command: protocol.CommandAnnouncement, Payload: announcement[0]})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := protocol.EncodeTraverse(embedded, backend.peerPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := protocol.EncodeTraverseSetAddress(payload, net.ParseIP("10.0.0.7").To4(), 1001, 0, nil, 0, 0); err != nil {
		t.Fatal(err)
	}
	msg, err := protocol.DecodeTraverse(&protocol.MessageRaw{PacketRaw: protocol.PacketRaw{Command: protocol.CommandTraverse, Payload: payload}, SenderPublicKey: relayPublicKey})
	if err != nil {
		t.Fatal(err)
	}

	relay.cmdTraverseReceive(msg, connectionRelay)

	if responseCount != 1 {
		t.Errorf("%d Responses sent to the original sender, expected 1", responseCount)
	}
	traverse.check(t, "answer to a received Traverse message", 0, nil, senderPublicKey)
}
