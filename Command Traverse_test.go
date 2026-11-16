/*
File Name:  Command Traverse_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"bytes"
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

	// The observed port and the reported external port are both returned.
	msg := &protocol.MessageTraverse{IPv4: net.ParseIP("203.0.113.7").To4(), PortIPv4: 1001, PortIPv4ReportedExternal: 2001, IPv6: net.ParseIP("2001:db8::7"), PortIPv6: 1002}
	if addresses := traverseAddresses(msg, false); len(addresses) != 2 || addresses[0].Port != 1001 || addresses[0].PortExternal != 2001 || addresses[1].Port != 1002 || addresses[1].PortExternal != 0 {
		t.Errorf("external port: unexpected addresses %v", addresses)
	}
	msg = &protocol.MessageTraverse{IPv4: net.ParseIP("203.0.113.7").To4(), PortIPv4: 1001, IPv6: net.ParseIP("2001:db8::7"), PortIPv6: 1002, PortIPv6ReportedExternal: 2002}
	if addresses := traverseAddresses(msg, false); len(addresses) != 2 || addresses[0].Port != 1001 || addresses[0].PortExternal != 0 || addresses[1].Port != 1002 || addresses[1].PortExternal != 2002 {
		t.Errorf("external port IPv6: unexpected addresses %v", addresses)
	}
}

// testTraverse returns the decoded Traverse message as the peer with the public key sends it to the relay: the packet of the original sender for the end receiver, embedded for the relay.
func testTraverse(t *testing.T, sender *protocol.KeyPair, receiverEnd, relay, traverseSender ed25519.PublicKey, command uint8, payload []byte) (msg *protocol.MessageTraverse) {
	embedded, err := protocol.PacketEncryptEmbedded(sender, receiverEnd, relay, &protocol.PacketRaw{Protocol: protocol.ProtocolVersion, Command: command, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}

	return testTraverseEmbedded(t, embedded, receiverEnd, traverseSender)
}

// testTraverseEmbedded returns the decoded Traverse message with the embedded packet as the peer with the public key sends it.
func testTraverseEmbedded(t *testing.T, embedded []byte, receiverEnd, traverseSender ed25519.PublicKey) (msg *protocol.MessageTraverse) {
	payload, err := protocol.EncodeTraverse(embedded, receiverEnd)
	if err != nil {
		t.Fatal(err)
	}
	msg, err = protocol.DecodeTraverse(&protocol.MessageRaw{PacketRaw: protocol.PacketRaw{Command: protocol.CommandTraverse, Payload: payload}, SenderPublicKey: traverseSender})
	if err != nil {
		t.Fatal(err)
	}

	return msg
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
		msg := testTraverse(t, testKeyPair(t, senderPrivateKey), targetPublicKey, backend.peerPublicKey, senderPublicKey, protocol.CommandAnnouncement, announcement[0])

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
	msg := testTraverse(t, testKeyPair(t, senderPrivateKey), backend.peerPublicKey, relayPublicKey, relayPublicKey, protocol.CommandAnnouncement, announcement[0])
	msg.IPv4, msg.PortIPv4 = net.ParseIP("10.0.0.7").To4(), 1001

	relay.cmdTraverseReceive(msg, connectionRelay)

	if responseCount != 1 {
		t.Errorf("%d Responses sent to the original sender, expected 1", responseCount)
	}
	traverse.check(t, "answer to a received Traverse message", 0, nil, senderPublicKey)
}

// TestTraverseForwardSender checks that the relay forwards a Traverse message only if the sender of the embedded packet is the sender of the Traverse message.
// The relay reads only the sender of the embedded packet: an embedded packet that is not an Announcement is forwarded.
func TestTraverseForwardSender(t *testing.T) {
	backend, network := testBackendLoopback(t)
	testRootPeers(t)

	var forwarded []*protocol.PacketRaw
	backend.Filters.PacketOut = func(packet *protocol.PacketRaw, receiverPublicKey ed25519.PublicKey, connection *Connection) {
		if packet.Command == protocol.CommandTraverse {
			forwarded = append(forwarded, packet)
		}
	}

	// The original sender is connected via a public address.
	senderPublicKey, senderPrivateKey, _ := ed25519.GenerateKey(rand.Reader)
	connectionSender := &Connection{Address: &net.UDPAddr{IP: net.ParseIP("203.0.113.7").To4(), Port: 1001}, PortInternal: 112}
	sender := &PeerInfo{Backend: backend, PublicKey: senderPublicKey, connectionActive: []*Connection{connectionSender}, connectionLatest: connectionSender}

	_, otherPrivateKey, _ := ed25519.GenerateKey(rand.Reader)
	otherPublicKey := otherPrivateKey.Public().(ed25519.PublicKey)

	// The end receiver is in the peer list.
	targetPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	_, addressTarget := testSocket(t)
	target, _ := backend.PeerlistAdd(targetPublicKey, &Connection{backend: backend, Network: network, Address: addressTarget, PortInternal: uint16(addressTarget.Port)})
	target.Features = 1 << protocol.FeatureIPv4Listen

	announcement := protocol.EncodeAnnouncement(true, &protocol.Requests{FindSelf: true}, 1<<protocol.FeatureIPv4Listen, 0, 0, "test")[0]

	// An embedded packet of the minimum size whose sender's public key does not decrypt to the sender of the Traverse message.
	random := make([]byte, protocol.PacketLengthMinEmbedded)
	rand.Read(random)

	tests := []struct {
		name      string
		msg       *protocol.MessageTraverse
		forwarded bool
	}{
		{"embedded Announcement of the sender", testTraverse(t, testKeyPair(t, senderPrivateKey), targetPublicKey, backend.peerPublicKey, senderPublicKey, protocol.CommandAnnouncement, announcement), true},
		{"embedded Ping of the sender", testTraverse(t, testKeyPair(t, senderPrivateKey), targetPublicKey, backend.peerPublicKey, senderPublicKey, protocol.CommandPing, nil), true},
		{"embedded Announcement of another peer", testTraverse(t, testKeyPair(t, otherPrivateKey), targetPublicKey, backend.peerPublicKey, senderPublicKey, protocol.CommandAnnouncement, announcement), false},
		{"embedded Announcement of the sender for another end receiver", testTraverseEmbedded(t, testTraverse(t, testKeyPair(t, senderPrivateKey), otherPublicKey, backend.peerPublicKey, senderPublicKey, protocol.CommandAnnouncement, announcement).EmbeddedPacketRaw, targetPublicKey, senderPublicKey), false},
		{"random embedded packet", testTraverseEmbedded(t, random, targetPublicKey, senderPublicKey), false},
	}

	for _, test := range tests {
		forwarded = nil
		sender.cmdTraverseForward(test.msg, connectionSender)

		if !test.forwarded && len(forwarded) != 0 {
			t.Errorf("%s: %d Traverse messages forwarded, expected none", test.name, len(forwarded))
		} else if test.forwarded && len(forwarded) != 1 {
			t.Errorf("%s: %d Traverse messages forwarded, expected 1", test.name, len(forwarded))
		} else if test.forwarded {
			// The embedded packet is forwarded unchanged, with the address of the original sender set.
			traverse, err := protocol.DecodeTraverse(&protocol.MessageRaw{PacketRaw: *forwarded[0]})
			if err != nil {
				t.Errorf("%s: %v", test.name, err)
			} else if !bytes.Equal(traverse.EmbeddedPacketRaw, test.msg.EmbeddedPacketRaw) || !traverse.TargetPeer.Equal(targetPublicKey) || !traverse.IPv4.Equal(connectionSender.Address.IP) || traverse.PortIPv4 != 1001 {
				t.Errorf("%s: forwarded Traverse message mismatch", test.name)
			}
		}
	}
}

// TestTraverseReceiveInvalid checks that the end receiver drops a Traverse message whose embedded packet is not an Announcement, was built for another relay or another end receiver,
// is a packet for direct sending, or was changed.
func TestTraverseReceiveInvalid(t *testing.T) {
	backend, network := testBackendLoopback(t)
	testRootPeers(t)

	responseCount := 0
	backend.Filters.MessageOutResponse = func(peer *PeerInfo, packet *protocol.PacketRaw, records []protocol.ResponseRecord) {
		responseCount++
	}

	// The relay is in the peer list, connected via a local connection.
	relayPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	_, addressRelay := testSocket(t)
	connectionRelay := &Connection{backend: backend, Network: network, Address: addressRelay, PortInternal: uint16(addressRelay.Port)}
	relay, _ := backend.PeerlistAdd(relayPublicKey, connectionRelay)

	otherPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)

	announcement := protocol.EncodeAnnouncement(true, &protocol.Requests{FindSelf: true}, 1<<protocol.FeatureIPv4Listen, 0, 0, "test")[0]

	tests := []struct {
		name     string
		accepted bool
		msg      func(sender *protocol.KeyPair) *protocol.MessageTraverse // the Traverse message as the relay delivers it
	}{
		{"embedded Announcement", true, func(sender *protocol.KeyPair) *protocol.MessageTraverse {
			return testTraverse(t, sender, backend.peerPublicKey, relayPublicKey, relayPublicKey, protocol.CommandAnnouncement, announcement)
		}},
		{"embedded Ping", false, func(sender *protocol.KeyPair) *protocol.MessageTraverse {
			return testTraverse(t, sender, backend.peerPublicKey, relayPublicKey, relayPublicKey, protocol.CommandPing, nil)
		}},
		{"embedded Response", false, func(sender *protocol.KeyPair) *protocol.MessageTraverse {
			return testTraverse(t, sender, backend.peerPublicKey, relayPublicKey, relayPublicKey, protocol.CommandResponse, announcement)
		}},
		{"embedded Announcement built for another relay", false, func(sender *protocol.KeyPair) *protocol.MessageTraverse {
			return testTraverse(t, sender, backend.peerPublicKey, otherPublicKey, relayPublicKey, protocol.CommandAnnouncement, announcement)
		}},
		{"embedded Announcement built for another end receiver", false, func(sender *protocol.KeyPair) *protocol.MessageTraverse {
			embedded := testTraverse(t, sender, otherPublicKey, relayPublicKey, relayPublicKey, protocol.CommandAnnouncement, announcement).EmbeddedPacketRaw
			return testTraverseEmbedded(t, embedded, backend.peerPublicKey, relayPublicKey)
		}},
		{"Announcement for direct sending", false, func(sender *protocol.KeyPair) *protocol.MessageTraverse {
			direct, err := protocol.PacketEncrypt(sender, backend.peerPublicKey, &protocol.PacketRaw{Protocol: protocol.ProtocolVersion, Command: protocol.CommandAnnouncement, Payload: announcement})
			if err != nil {
				t.Fatal(err)
			}
			return testTraverseEmbedded(t, direct, backend.peerPublicKey, relayPublicKey)
		}},
		{"embedded Announcement with a changed byte", false, func(sender *protocol.KeyPair) *protocol.MessageTraverse {
			msg := testTraverse(t, sender, backend.peerPublicKey, relayPublicKey, relayPublicKey, protocol.CommandAnnouncement, announcement)
			msg.EmbeddedPacketRaw[len(msg.EmbeddedPacketRaw)-1] ^= 1
			return msg
		}},
	}

	for _, test := range tests {
		// Each case has its own original sender. Its address is a local one, which is used since the message is received via a local connection.
		_, senderPrivateKey, _ := ed25519.GenerateKey(rand.Reader)

		msg := test.msg(testKeyPair(t, senderPrivateKey))
		msg.IPv4, msg.PortIPv4 = net.ParseIP("10.0.0.7").To4(), 1001

		responseCount = 0
		relay.cmdTraverseReceive(msg, connectionRelay)

		if test.accepted && responseCount != 1 {
			t.Errorf("%s: %d Responses sent, expected 1", test.name, responseCount)
		} else if !test.accepted && responseCount != 0 {
			t.Errorf("%s: %d Responses sent, expected none", test.name, responseCount)
		}
	}
}
