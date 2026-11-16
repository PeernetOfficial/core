/*
File Name:  Network_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/hex"
	"net"
	"testing"

	"filippo.io/edwards25519"
	"github.com/PeernetOfficial/core/protocol"
	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/chacha20poly1305"
	"lukechampine.com/blake3"
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
	raw, err := protocol.PacketEncrypt(testKeyPair(t, senderPrivateKey), receiverPublicKey, &protocol.PacketRaw{Protocol: protocol.ProtocolVersion, Command: command, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// testPacketForged returns a packet for the fixed key pair in the name of the sender. It is built with the private key of the fixed key pair, without the private key of the sender.
func testPacketForged(t *testing.T, fixedPrivateKeyHex string, fixed *protocol.KeyPair, senderPublicKey ed25519.PublicKey, command uint8, payload []byte) (raw []byte) {
	seed, err := hex.DecodeString(fixedPrivateKeyHex)
	if err != nil {
		t.Fatal(err)
	}

	// The shared secret is the X25519 function of the fixed private key and the public key of the sender.
	hash := sha512.Sum512(seed)
	x25519PrivateKey, err := ecdh.X25519().NewPrivateKey(hash[:32])
	if err != nil {
		t.Fatal(err)
	}
	point, err := new(edwards25519.Point).SetBytes(senderPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	x25519PublicKey, err := ecdh.X25519().NewPublicKey(point.BytesMontgomery())
	if err != nil {
		t.Fatal(err)
	}
	sharedSecret, err := x25519PrivateKey.ECDH(x25519PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	key := blake3.Sum256(append(append(sharedSecret, senderPublicKey...), fixed.PublicKey...))
	aead, err := chacha20poly1305.New(key[:])
	if err != nil {
		t.Fatal(err)
	}

	// nonce, encrypted sender's public key, encrypted fields and payload, authentication tag
	raw = make([]byte, 12+32)
	rand.Read(raw[:12])

	cipher, err := chacha20.NewUnauthenticatedCipher(fixed.PublicKey, raw[:12])
	if err != nil {
		t.Fatal(err)
	}
	cipher.XORKeyStream(raw[12:44], senderPublicKey)

	fields := append([]byte{protocol.ProtocolVersion, command, 0, 0, 0, 0, byte(len(payload)), byte(len(payload) >> 8)}, payload...)
	raw = aead.Seal(raw, raw[:12], fields, nil)

	// The packet decrypts with the fixed key pair as a packet of the sender.
	if decoded, sender, err := protocol.PacketDecrypt(raw, fixed); err != nil || !sender.Equal(senderPublicKey) || decoded.Command != command {
		t.Fatalf("packet built with the fixed private key does not decrypt as packet of the sender: %v", err)
	}

	return raw
}

// TestLocalDiscovery checks that a Local Discovery message received via IPv4 broadcast or IPv6 multicast is accepted and answered with an Announcement to the sender,
// and that it is dropped if its signature is missing or does not verify, if it was received via unicast, and that any other message received via broadcast or multicast is dropped.
// The private keys of the fixed key pairs are known to everyone: a packet built with one of them in the name of another peer decrypts, and is dropped since it carries no signature of that peer.
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
		keyPair        *protocol.KeyPair
		privateKeyHex  string
	}{
		{"IPv4 broadcast", ipv4BroadcastPublicKey, ipv6MulticastPublicKey, ipv4BroadcastKeyPair, ipv4BroadcastPrivateKeyH},
		{"IPv6 multicast", ipv6MulticastPublicKey, ipv4BroadcastPublicKey, ipv6MulticastKeyPair, ipv6MulticastPrivateKeyH},
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
			{"built with the fixed private key in the name of the sender, signed by another peer", false, false, func(senderPrivateKey ed25519.PrivateKey) []byte {
				return testPacketForged(t, fixed.privateKeyHex, fixed.keyPair, senderPrivateKey.Public().(ed25519.PublicKey), protocol.CommandLocalDiscovery, protocol.LocalDiscoverySign(announcement, otherPrivateKey, fixed.publicKey))
			}},
			{"built with the fixed private key in the name of the sender, without signature", false, false, func(senderPrivateKey ed25519.PrivateKey) []byte {
				return testPacketForged(t, fixed.privateKeyHex, fixed.keyPair, senderPrivateKey.Public().(ed25519.PublicKey), protocol.CommandLocalDiscovery, announcement)
			}},
			{"Announcement built with the fixed private key in the name of the sender", false, false, func(senderPrivateKey ed25519.PrivateKey) []byte {
				return testPacketForged(t, fixed.privateKeyHex, fixed.keyPair, senderPrivateKey.Public().(ed25519.PublicKey), protocol.CommandAnnouncement, announcement)
			}},
		}

		for _, test := range tests {
			name := fixed.name + ", " + test.name

			// Each case has its own sender.
			senderPublicKey, senderPrivateKey, _ := ed25519.GenerateKey(rand.Reader)
			socketSender, addressSender := testSocket(t)

			receiverKeyPair := fixed.keyPair
			if test.unicast {
				receiverKeyPair = backend.peerKeyPair
			}

			messageInSender = nil
			testPacketWorker(backend, networkWire{network: network, sender: &net.UDPAddr{IP: addressSender.IP, Port: addressSender.Port}, raw: test.raw(senderPrivateKey), receiverKeyPair: receiverKeyPair, unicast: test.unicast})

			inPeerList := backend.PeerlistLookup(senderPublicKey) != nil

			if test.accepted {
				if !inPeerList || !messageInSender.Equal(senderPublicKey) {
					t.Errorf("%s: message not accepted (sender in peer list: %t)", name, inPeerList)
					continue
				}
				testExpectAnnouncement(t, name, backend, socketSender, senderPrivateKey)
			} else if inPeerList || messageInSender != nil {
				t.Errorf("%s: message accepted (sender in peer list: %t)", name, inPeerList)
			}
		}
	}
}
