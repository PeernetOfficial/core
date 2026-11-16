/*
File Name:  Network_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"net"
	"testing"

	"github.com/PeernetOfficial/core/protocol"
)

// testPacketWorker lets the packet worker process the incoming packets and returns when it is done.
func testPacketWorker(backend *Backend, packets ...networkWire) {
	backend.networks.rawPacketsIncoming = make(chan networkWire, len(packets))
	for _, packet := range packets {
		backend.networks.rawPacketsIncoming <- packet
	}
	close(backend.networks.rawPacketsIncoming)

	backend.networks.packetWorker()
}

// testPacket returns the packet as the sender transmits it to the receiver.
func testPacket(t *testing.T, senderPrivateKey ed25519.PrivateKey, receiverPublicKey ed25519.PublicKey, command uint8, payload []byte) (raw []byte) {
	raw, err := protocol.PacketEncrypt(senderPrivateKey, receiverPublicKey, &protocol.PacketRaw{Protocol: protocol.ProtocolVersion, Command: command, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestLocalDiscovery checks that a Local Discovery message received via IPv4 broadcast or IPv6 multicast is accepted and answered with an Announcement to the sender,
// and that it is dropped if its signature is missing or does not verify, if it was received via unicast, and that any other message received via broadcast or multicast is dropped.
func TestLocalDiscovery(t *testing.T) {
	initBroadcastIPv4()
	initMulticastIPv6()

	// The fixed public keys are the ones of the whitepaper.
	if key := hex.EncodeToString(ipv4BroadcastPublicKey); key != "7e1e727b62c6a791b0b3fdd18c957c985f24b53c2e23425f10de143469c7df7b" {
		t.Fatalf("IPv4 broadcast public key is %s", key)
	}
	if key := hex.EncodeToString(ipv6MulticastPublicKey); key != "cacfa174378dceb20bc46f314d4dc6ebbc9ccadc9961dadf0a0ba183ea1df23d" {
		t.Fatalf("IPv6 multicast public key is %s", key)
	}

	backend, network := testBackendLoopback(t)
	testRootPeers(t)

	var messageInSender ed25519.PublicKey
	backend.Filters.MessageIn = func(peer *PeerInfo, raw *protocol.MessageRaw, message interface{}) {
		if _, ok := message.(*protocol.MessageAnnouncement); ok && raw.Command == protocol.CommandLocalDiscovery {
			messageInSender = peer.PublicKey
		}
	}

	announcement := protocol.EncodeAnnouncement(true, nil, 1<<protocol.FeatureIPv4Listen, 0, 0, "test")[0]

	fixedKeys := []struct {
		name           string
		publicKey      ed25519.PublicKey
		publicKeyOther ed25519.PublicKey
	}{
		{"IPv4 broadcast", ipv4BroadcastPublicKey, ipv6MulticastPublicKey},
		{"IPv6 multicast", ipv6MulticastPublicKey, ipv4BroadcastPublicKey},
	}

	for _, fixed := range fixedKeys {
		_, otherPrivateKey, _ := ed25519.GenerateKey(rand.Reader)

		tests := []struct {
			name     string
			unicast  bool
			accepted bool
			raw      func(senderPrivateKey ed25519.PrivateKey) []byte // the packet as received from the sender
		}{
			{"signed by the sender", false, true, func(senderPrivateKey ed25519.PrivateKey) []byte {
				return testPacket(t, senderPrivateKey, fixed.publicKey, protocol.CommandLocalDiscovery, protocol.LocalDiscoverySign(announcement, senderPrivateKey, fixed.publicKey))
			}},
			{"signed by another peer", false, false, func(senderPrivateKey ed25519.PrivateKey) []byte {
				return testPacket(t, senderPrivateKey, fixed.publicKey, protocol.CommandLocalDiscovery, protocol.LocalDiscoverySign(announcement, otherPrivateKey, fixed.publicKey))
			}},
			{"signed for the other fixed public key", false, false, func(senderPrivateKey ed25519.PrivateKey) []byte {
				return testPacket(t, senderPrivateKey, fixed.publicKey, protocol.CommandLocalDiscovery, protocol.LocalDiscoverySign(announcement, senderPrivateKey, fixed.publicKeyOther))
			}},
			{"without signature", false, false, func(senderPrivateKey ed25519.PrivateKey) []byte {
				return testPacket(t, senderPrivateKey, fixed.publicKey, protocol.CommandLocalDiscovery, announcement)
			}},
			{"without payload", false, false, func(senderPrivateKey ed25519.PrivateKey) []byte {
				return testPacket(t, senderPrivateKey, fixed.publicKey, protocol.CommandLocalDiscovery, nil)
			}},
			{"received via unicast", true, false, func(senderPrivateKey ed25519.PrivateKey) []byte {
				return testPacket(t, senderPrivateKey, backend.peerPublicKey, protocol.CommandLocalDiscovery, protocol.LocalDiscoverySign(announcement, senderPrivateKey, fixed.publicKey))
			}},
			{"Announcement instead of Local Discovery", false, false, func(senderPrivateKey ed25519.PrivateKey) []byte {
				return testPacket(t, senderPrivateKey, fixed.publicKey, protocol.CommandAnnouncement, announcement)
			}},
			{"Ping instead of Local Discovery", false, false, func(senderPrivateKey ed25519.PrivateKey) []byte {
				return testPacket(t, senderPrivateKey, fixed.publicKey, protocol.CommandPing, nil)
			}},
		}

		for _, test := range tests {
			name := fixed.name + ", " + test.name

			// Each case has its own sender.
			senderPublicKey, senderPrivateKey, _ := ed25519.GenerateKey(rand.Reader)
			socketSender, addressSender := testSocket(t)

			receiverPublicKey := fixed.publicKey
			if test.unicast {
				receiverPublicKey = backend.peerPublicKey
			}

			messageInSender = nil
			testPacketWorker(backend, networkWire{network: network, sender: &net.UDPAddr{IP: addressSender.IP, Port: addressSender.Port}, raw: test.raw(senderPrivateKey), receiverPublicKey: receiverPublicKey, unicast: test.unicast})

			inPeerList := backend.PeerlistLookup(senderPublicKey) != nil

			if test.accepted {
				if !inPeerList || !messageInSender.Equal(senderPublicKey) {
					t.Errorf("%s: message not accepted (sender in peer list: %t)", name, inPeerList)
					continue
				}
				testExpectAnnouncement(t, name, backend, socketSender, senderPublicKey)
			} else if inPeerList || messageInSender != nil {
				t.Errorf("%s: message accepted (sender in peer list: %t)", name, inPeerList)
			}
		}
	}
}
