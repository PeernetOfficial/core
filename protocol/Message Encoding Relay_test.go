/*
File Name:  Message Encoding Relay_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package protocol

import (
	"bytes"
	"crypto/ed25519"
	"testing"
)

// TestRelayVector checks the bytes of a packet embedded in a Relay message against a test vector that was created independently from the whitepaper.
// The keys, the nonce and the packet are those of TestPacketVector.
func TestRelayVector(t *testing.T) {
	sender := newKeyPair(t, ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32)))
	receiver := newKeyPair(t, ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, 32)))
	relay := newKeyPair(t, ed25519.NewKeyFromSeed(bytes.Repeat([]byte{3}, 32)))

	nonce := fromHex(t, "000102030405060708090a0b")
	packet := &PacketRaw{Protocol: 0, Command: 1, Sequence: 0x01020304, Payload: []byte("Peernet")}

	vectorRelay := fromHex(t, "000102030405060708090a0bee9cd13863e0a5fdb40c6b5e10a5c73b69ed48eec31c0d15efbde390d0a2c1")

	embedded, err := packetEncryptRelay(sender, receiver.PublicKey, nonce, embeddedAdditionalData(relay.PublicKey, CommandRelay), packet)
	if err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(embedded, vectorRelay) {
		t.Errorf("embedded packet is\n%x, expected\n%x", embedded, vectorRelay)
	}

	decoded, err := PacketDecryptRelay(vectorRelay, receiver, sender.PublicKey, relay.PublicKey, nil)
	if err != nil {
		t.Fatalf("embedded packet: %v", err)
	} else if decoded.Protocol != 0 || decoded.Command != 1 || decoded.Sequence != 0x01020304 || string(decoded.Payload) != "Peernet" {
		t.Errorf("embedded packet decoded as %+v", decoded)
	}
}

// TestRelayPacket verifies that a packet embedded in a Relay message is decoded by its end receiver with the same fields, with and without the cache of the packet keys,
// and that it is bound to its sender, its end receiver and its relay. A packet embedded in a Traverse message, a direct packet, both without the sender's public key,
// a changed packet, a packet with randomized garbage and a packet of any size below the minimum are rejected.
func TestRelayPacket(t *testing.T) {
	sender := generateKeyPair(t)
	receiver := generateKeyPair(t)
	relay := generateKeyPair(t)
	other := generateKeyPair(t)

	for _, payload := range [][]byte{nil, []byte("hello"), bytes.Repeat([]byte{7}, 1000)} {
		packet := &PacketRaw{Protocol: ProtocolVersion, Command: CommandResponse, Sequence: 0xA1B2C3D4, Payload: payload}

		embedded, err := PacketEncryptRelay(sender, receiver.PublicKey, relay.PublicKey, packet)
		if err != nil {
			t.Fatal(err)
		} else if len(embedded) != PacketLengthMinRelay+len(payload) {
			t.Fatalf("embedded packet has %d bytes, expected %d", len(embedded), PacketLengthMinRelay+len(payload))
		}

		keys := &PacketKeys{}
		for n := 0; n < 2; n++ { // the second run uses the cached packet key
			for _, cache := range []*PacketKeys{nil, keys} {
				decoded, err := PacketDecryptRelay(embedded, receiver, sender.PublicKey, relay.PublicKey, cache)
				if err != nil {
					t.Fatal(err)
				} else if decoded.Protocol != ProtocolVersion || decoded.Command != CommandResponse || decoded.Sequence != 0xA1B2C3D4 || !bytes.Equal(decoded.Payload, payload) {
					t.Fatalf("embedded packet decoded as %+v", decoded)
				}
			}
		}

		// The cached packet key of the sender is not used for an embedded packet of another sender.
		if _, err := PacketDecryptRelay(embedded, receiver, other.PublicKey, relay.PublicKey, keys); err == nil || err.Error() != "invalid authentication tag" {
			t.Fatalf("embedded packet with another sender and the cached key: %v, expected invalid authentication tag", err)
		}
	}

	packet := &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 1, Payload: []byte("hello")}
	embedded, err := PacketEncryptRelay(sender, receiver.PublicKey, relay.PublicKey, packet)
	if err != nil {
		t.Fatal(err)
	}

	// withoutSender returns a packet without the sender's public key.
	withoutSender := func(raw []byte, err error) []byte {
		if err != nil {
			t.Fatal(err)
		}
		return append(append([]byte{}, raw[:packetNonceSize]...), raw[packetOffsetEncrypted:]...)
	}

	key, err := sender.packetKeyCached(nil, receiver.PublicKey, true)
	if err != nil {
		t.Fatal(err)
	}
	garbage, err := packetEncryptFields(append([]byte{}, embedded[:packetNonceSize]...), embedded[:packetNonceSize], key, embeddedAdditionalData(relay.PublicKey, CommandRelay), packet, []byte{1})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		raw      []byte
		receiver *KeyPair
		sender   ed25519.PublicKey
		relay    ed25519.PublicKey
		expected string
	}{
		{"another sender", embedded, receiver, other.PublicKey, relay.PublicKey, "invalid authentication tag"},
		{"another end receiver", embedded, other, sender.PublicKey, relay.PublicKey, "invalid authentication tag"},
		{"another relay", embedded, receiver, sender.PublicKey, other.PublicKey, "invalid authentication tag"},
		{"changed byte", flipBit(embedded, len(embedded)*8-1), receiver, sender.PublicKey, relay.PublicKey, "invalid authentication tag"},
		{"changed nonce", flipBit(embedded, 0), receiver, sender.PublicKey, relay.PublicKey, "invalid authentication tag"},
		{"packet embedded in a Traverse message", withoutSender(PacketEncryptEmbedded(sender, receiver.PublicKey, relay.PublicKey, packet)), receiver, sender.PublicKey, relay.PublicKey, "invalid authentication tag"},
		{"direct packet without randomized garbage", withoutSender(packetEncrypt(sender, receiver.PublicKey, embedded[:packetNonceSize], nil, packet, nil, nil)), receiver, sender.PublicKey, relay.PublicKey, "invalid authentication tag"},
		{"randomized garbage", garbage, receiver, sender.PublicKey, relay.PublicKey, "embedded packet: invalid length"},
		{"sender is not a valid public key", embedded, receiver, make(ed25519.PublicKey, 31), relay.PublicKey, "invalid public key"},
		{"relay is not a valid public key", embedded, receiver, sender.PublicKey, make(ed25519.PublicKey, 31), "embedded packet: invalid relay peer ID"},
		{"no key pair of the receiver", embedded, nil, sender.PublicKey, relay.PublicKey, "invalid receiver key pair"},
	}

	for _, test := range tests {
		if _, err := PacketDecryptRelay(test.raw, test.receiver, test.sender, test.relay, nil); err == nil || err.Error() != test.expected {
			t.Errorf("%s: %v, expected %s", test.name, err, test.expected)
		}
	}

	// A packet embedded in a Relay message is rejected inside a Traverse message, with the sender's public key added, and as a direct packet.
	withSender, err := packetEncrypt(sender, receiver.PublicKey, embedded[:packetNonceSize], embeddedAdditionalData(relay.PublicKey, CommandRelay), packet, nil, nil)
	if err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(withoutSender(withSender, nil), embedded) {
		t.Fatal("the packet with the sender's public key differs in other fields")
	}
	if _, _, err := PacketDecryptEmbedded(withSender, receiver, relay.PublicKey); err == nil || err.Error() != "invalid authentication tag" {
		t.Errorf("inside a Traverse message: %v, expected invalid authentication tag", err)
	}
	if _, _, err := PacketDecrypt(withSender, receiver, nil); err == nil || err.Error() != "invalid authentication tag" {
		t.Errorf("as direct packet: %v, expected invalid authentication tag", err)
	}

	// Any size below the minimum is rejected.
	minimum, err := PacketEncryptRelay(sender, receiver.PublicKey, relay.PublicKey, &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 1})
	if err != nil {
		t.Fatal(err)
	} else if len(minimum) != PacketLengthMinRelay || PacketLengthMinRelay != 36 {
		t.Fatalf("embedded packet without payload has %d bytes, minimum is %d, expected 36", len(minimum), PacketLengthMinRelay)
	}
	for n := 0; n < PacketLengthMinRelay; n++ {
		if _, err := PacketDecryptRelay(minimum[:n], receiver, sender.PublicKey, relay.PublicKey, nil); err == nil {
			t.Fatalf("embedded packet of %d bytes accepted", n)
		}
	}

	// Two embedded packets with the same content have their own nonce.
	second, err := PacketEncryptRelay(sender, receiver.PublicKey, relay.PublicKey, packet)
	if err != nil {
		t.Fatal(err)
	} else if bytes.Equal(embedded[:packetNonceSize], second[:packetNonceSize]) {
		t.Error("two embedded packets have the same nonce")
	}

	if _, err := PacketEncryptRelay(nil, receiver.PublicKey, relay.PublicKey, packet); err == nil {
		t.Error("packet created without key pair of the sender")
	}
	if _, err := PacketEncryptRelay(sender, receiver.PublicKey, make(ed25519.PublicKey, 31), packet); err == nil {
		t.Error("packet created with an invalid relay")
	}
}

// TestRelayLayout checks the payload layout: Peer ID of the other peer (32), embedded packet.
func TestRelayLayout(t *testing.T) {
	sender := generateKeyPair(t)
	relay := generateKeyPair(t)
	target := generateKeyPair(t)

	embedded, err := PacketEncryptRelay(sender, target.PublicKey, relay.PublicKey, &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 7, Payload: []byte("hello")})
	if err != nil {
		t.Fatal(err)
	}

	// The original sender names the end receiver.
	payload, err := EncodeRelay(embedded, target.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) != 32+len(embedded) || !bytes.Equal(payload[0:32], target.PublicKey) || !bytes.Equal(payload[32:], embedded) {
		t.Fatal("Peer ID or embedded packet at wrong offset")
	}

	msg, err := DecodeRelay(&MessageRaw{PacketRaw: PacketRaw{Command: CommandRelay, Payload: payload}, SenderPublicKey: sender.PublicKey})
	if err != nil {
		t.Fatal(err)
	} else if !msg.Peer.Equal(target.PublicKey) || !bytes.Equal(msg.EmbeddedPacketRaw, embedded) {
		t.Fatal("Peer ID or embedded packet mismatch")
	}

	// The relay names the original sender and forwards the embedded packet unchanged. The end receiver decrypts it.
	payload, err = EncodeRelay(msg.EmbeddedPacketRaw, msg.SenderPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if msg, err = DecodeRelay(&MessageRaw{PacketRaw: PacketRaw{Command: CommandRelay, Payload: payload}, SenderPublicKey: relay.PublicKey}); err != nil {
		t.Fatal(err)
	}

	decoded, err := PacketDecryptRelay(msg.EmbeddedPacketRaw, target, msg.Peer, msg.SenderPublicKey, nil)
	if err != nil {
		t.Fatal(err)
	} else if !msg.Peer.Equal(sender.PublicKey) || decoded.Command != CommandAnnouncement || decoded.Sequence != 7 || !bytes.Equal(decoded.Payload, []byte("hello")) {
		t.Fatal("embedded packet mismatch")
	}

	// A payload with an embedded packet below the minimum size is rejected.
	short := 32 + PacketLengthMinRelay - 1
	if _, err := DecodeRelay(&MessageRaw{PacketRaw: PacketRaw{Command: CommandRelay, Payload: payload[:short]}}); err == nil {
		t.Fatalf("payload of %d bytes accepted", short)
	}
	if _, err := EncodeRelay(embedded[:PacketLengthMinRelay-1], target.PublicKey); err == nil {
		t.Fatal("embedded packet below the minimum size encoded")
	}
	if _, err := EncodeRelay(embedded, target.PublicKey[:31]); err == nil {
		t.Fatal("invalid Peer ID encoded")
	}
	if _, err := EncodeRelay(make([]byte, udpMaxPacketSize), target.PublicKey); err == nil {
		t.Fatal("embedded packet above the maximum size encoded")
	}
}
