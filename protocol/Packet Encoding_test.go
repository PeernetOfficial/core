/*
File Name:  Packet Encoding_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package protocol

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"net"
	"sync"
	"testing"

	"filippo.io/edwards25519"
	"golang.org/x/crypto/chacha20poly1305"
)

func generateKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return publicKey, privateKey
}

// generateKeyPair returns a new key pair for encrypting and decrypting packets.
func generateKeyPair(t *testing.T) (keyPair *KeyPair) {
	_, privateKey := generateKey(t)
	return newKeyPair(t, privateKey)
}

func newKeyPair(t *testing.T, privateKey ed25519.PrivateKey) (keyPair *KeyPair) {
	keyPair, err := NewKeyPair(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return keyPair
}

func fromHex(t *testing.T, text string) []byte {
	data, err := hex.DecodeString(text)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// flipBit returns a copy of the data with one bit flipped.
func flipBit(data []byte, bit int) (result []byte) {
	result = append([]byte{}, data...)
	result[bit/8] ^= 1 << (bit % 8)
	return result
}

// TestPacketVector checks the key derivation and the bytes of a direct and an embedded packet against a test vector that was created independently from the whitepaper.
func TestPacketVector(t *testing.T) {
	sender := newKeyPair(t, ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32)))
	receiver := newKeyPair(t, ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, 32)))
	relay := newKeyPair(t, ed25519.NewKeyFromSeed(bytes.Repeat([]byte{3}, 32)))

	nonce := fromHex(t, "000102030405060708090a0b")
	packet := &PacketRaw{Protocol: 0, Command: 1, Sequence: 0x01020304, Payload: []byte("Peernet")}

	vectorDirect := fromHex(t, "000102030405060708090a0b973aaf461f9e9e39c748b95c1c9f40e79bf35d140d613746347134957ee92b34ee9cd13863e0a5fdb40c6b5e10a5c7017083798785b26c6d065bdb6d21d441")
	vectorEmbedded := fromHex(t, "000102030405060708090a0b973aaf461f9e9e39c748b95c1c9f40e79bf35d140d613746347134957ee92b34ee9cd13863e0a5fdb40c6b5e10a5c701453429f72b9ab6b167e058f5cd229a")

	// keys
	for _, test := range []struct {
		name     string
		got      []byte
		expected string
	}{
		{"sender Peer ID", sender.PublicKey, "8a88e3dd7409f195fd52db2d3cba5d72ca6709bf1d94121bf3748801b40f6f5c"},
		{"receiver Peer ID", receiver.PublicKey, "8139770ea87d175f56a35466c34c7ecccb8d8a91b4ee37a25df60f5b8fc9b394"},
		{"relay Peer ID", relay.PublicKey, "ed4928c628d1c2c6eae90338905995612959273a5c63f93636c14614ac8737d1"},
		{"sender X25519 private key", sender.x25519.Bytes(), "5ce86efb75fa4e2c410f46e16de9f6acae1a1703528651b69bc176c088bef3ee"},
		{"sender X25519 public key", sender.x25519.PublicKey().Bytes(), "1b1b58dd50ea14b60da17b790cd02754d970c9bab864ebb3c0f3016fe51d3f57"},
		{"receiver X25519 public key", receiver.x25519.PublicKey().Bytes(), "60346e7c911a5f6ba154129174cafe75b294ac3bbd5549632f48cec6266f8410"},
	} {
		if hex.EncodeToString(test.got) != test.expected {
			t.Errorf("%s is %x, expected %s", test.name, test.got, test.expected)
		}
	}

	// The X25519 public key that is converted from the Peer ID is the public key of the X25519 private key.
	for _, keyPair := range []*KeyPair{sender, receiver, relay} {
		point, err := new(edwards25519.Point).SetBytes(keyPair.PublicKey)
		if err != nil {
			t.Fatal(err)
		} else if !bytes.Equal(point.BytesMontgomery(), keyPair.x25519.PublicKey().Bytes()) {
			t.Errorf("converted X25519 public key of %x is %x, expected %x", keyPair.PublicKey, point.BytesMontgomery(), keyPair.x25519.PublicKey().Bytes())
		}
	}

	// The sender and the receiver derive the same packet key. The key of the other direction is another one.
	const vectorPacketKey = "f70ceef250387a6627662a74f7aa920ae5555e83171fdc9c0d5c6ac14e9b1ba0"
	keySender, err := sender.packetKeyCached(nil, receiver.PublicKey, true)
	if err != nil {
		t.Fatal(err)
	}
	keyReceiver, err := receiver.packetKeyCached(nil, sender.PublicKey, false)
	if err != nil {
		t.Fatal(err)
	}
	keyReverse, err := receiver.packetKeyCached(nil, sender.PublicKey, true)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(keySender) != vectorPacketKey || hex.EncodeToString(keyReceiver) != vectorPacketKey {
		t.Errorf("packet key is %x at the sender and %x at the receiver, expected %s", keySender, keyReceiver, vectorPacketKey)
	}
	if bytes.Equal(keyReverse, keySender) {
		t.Error("the packet key of the other direction is the same")
	}

	// encode
	direct, err := packetEncrypt(sender, receiver.PublicKey, nonce, nil, packet, nil, nil)
	if err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(direct, vectorDirect) {
		t.Errorf("direct packet is\n%x, expected\n%x", direct, vectorDirect)
	}

	embedded, err := packetEncrypt(sender, receiver.PublicKey, nonce, embeddedAdditionalData(relay.PublicKey, CommandTraverse), packet, nil, nil)
	if err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(embedded, vectorEmbedded) {
		t.Errorf("embedded packet is\n%x, expected\n%x", embedded, vectorEmbedded)
	}

	// decode
	decoded, senderPublicKey, err := PacketDecrypt(vectorDirect, receiver, nil)
	if err != nil {
		t.Fatalf("direct packet: %v", err)
	} else if !senderPublicKey.Equal(sender.PublicKey) || decoded.Protocol != 0 || decoded.Command != 1 || decoded.Sequence != 0x01020304 || string(decoded.Payload) != "Peernet" {
		t.Errorf("direct packet decoded as %+v from %x", decoded, senderPublicKey)
	}

	decoded, senderPublicKey, err = PacketDecryptEmbedded(vectorEmbedded, receiver, relay.PublicKey)
	if err != nil {
		t.Fatalf("embedded packet: %v", err)
	} else if !senderPublicKey.Equal(sender.PublicKey) || decoded.Protocol != 0 || decoded.Command != 1 || decoded.Sequence != 0x01020304 || string(decoded.Payload) != "Peernet" {
		t.Errorf("embedded packet decoded as %+v from %x", decoded, senderPublicKey)
	}

	if embeddedSender, err := PacketEmbeddedSender(vectorEmbedded, receiver.PublicKey); err != nil || !embeddedSender.Equal(sender.PublicKey) {
		t.Errorf("sender of the embedded packet read as %x: %v", embeddedSender, err)
	}
}

// TestPacketRoundTrip encrypts direct packets, with and without randomized garbage, and embedded packets, and verifies that the receiver decodes the same fields and identifies the sender from the packet.
func TestPacketRoundTrip(t *testing.T) {
	sender := generateKeyPair(t)
	receiver := generateKeyPair(t)
	relay := generateKeyPair(t)

	if PacketLengthMin != 68 || PacketLengthMinEmbedded != 68 {
		t.Fatalf("minimum packet size %d, embedded %d, expected 68", PacketLengthMin, PacketLengthMinEmbedded)
	}

	check := func(name string, packet, decoded *PacketRaw, senderPublicKey ed25519.PublicKey, err error) {
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		} else if !senderPublicKey.Equal(sender.PublicKey) {
			t.Fatalf("%s: sender public key mismatch", name)
		} else if decoded.Protocol != packet.Protocol || decoded.Command != packet.Command || decoded.Sequence != packet.Sequence || !bytes.Equal(decoded.Payload, packet.Payload) {
			t.Fatalf("%s: decoded packet mismatch: %+v", name, decoded)
		}
	}

	for _, payload := range [][]byte{nil, []byte("payload"), make([]byte, 440), make([]byte, 1164), make([]byte, 65535-PacketLengthMin)} {
		packet := &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 0xFEDCBA98, Payload: payload}

		// direct, as sent
		raw, err := PacketEncrypt(sender, receiver.PublicKey, packet, nil)
		if err != nil {
			t.Fatal(err)
		} else if len(raw) < PacketLengthMin+len(payload) || len(raw) >= PacketLengthMin+len(payload)+maxRandomGarbage {
			t.Fatalf("direct packet with %d bytes payload has %d bytes", len(payload), len(raw))
		}
		decoded, senderPublicKey, err := PacketDecrypt(raw, receiver, nil)
		check("direct", packet, decoded, senderPublicKey, err)

		// direct, with every size of randomized garbage
		for sizeGarbage := 0; sizeGarbage < maxRandomGarbage; sizeGarbage++ {
			nonce := make([]byte, packetNonceSize)
			rand.Read(nonce)
			garbage := make([]byte, sizeGarbage)
			rand.Read(garbage)

			raw, err := packetEncrypt(sender, receiver.PublicKey, nonce, nil, packet, garbage, nil)
			if err != nil {
				t.Fatal(err)
			} else if len(raw) != PacketLengthMin+len(payload)+sizeGarbage {
				t.Fatalf("direct packet with %d bytes payload and %d bytes garbage has %d bytes", len(payload), sizeGarbage, len(raw))
			}
			decoded, senderPublicKey, err := PacketDecrypt(raw, receiver, nil)
			check("direct with garbage", packet, decoded, senderPublicKey, err)
		}

		// embedded
		raw, err = PacketEncryptEmbedded(sender, receiver.PublicKey, relay.PublicKey, packet)
		if err != nil {
			t.Fatal(err)
		} else if len(raw) != PacketLengthMinEmbedded+len(payload) {
			t.Fatalf("embedded packet with %d bytes payload has %d bytes", len(payload), len(raw))
		}
		decoded, senderPublicKey, err = PacketDecryptEmbedded(raw, receiver, relay.PublicKey)
		check("embedded", packet, decoded, senderPublicKey, err)

		if embeddedSender, err := PacketEmbeddedSender(raw, receiver.PublicKey); err != nil || !embeddedSender.Equal(sender.PublicKey) {
			t.Fatalf("sender of the embedded packet read as %x: %v", embeddedSender, err)
		}
	}
}

// TestPacketOtherReceiver verifies that a packet for one receiver is rejected by another peer, as is and with the sender's public key encrypted again for that peer.
func TestPacketOtherReceiver(t *testing.T) {
	sender := generateKeyPair(t)
	receiver := generateKeyPair(t)
	other := generateKeyPair(t)

	packet := &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 1, Payload: []byte("payload")}

	raw, err := PacketEncrypt(sender, receiver.PublicKey, packet, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := PacketDecrypt(raw, other, nil); err == nil {
		t.Fatal("packet accepted by another peer")
	}

	// The sender's public key is readable with the receiver's Peer ID. Encrypt it again for the other peer, which then derives the packet key for this sender.
	redirected := append([]byte{}, raw...)
	if err := packetSenderXOR(redirected[packetNonceSize:packetOffsetEncrypted], sender.PublicKey, raw[:packetNonceSize], other.PublicKey); err != nil {
		t.Fatal(err)
	}
	if redirectedSender, err := PacketEmbeddedSender(redirected, other.PublicKey); err != nil || !redirectedSender.Equal(sender.PublicKey) {
		t.Fatalf("redirected packet does not carry the sender for the other peer: %v", err)
	}
	if _, _, err := PacketDecrypt(redirected, other, nil); err == nil || err.Error() != "invalid authentication tag" {
		t.Fatalf("redirected packet at another peer: %v, expected invalid authentication tag", err)
	}

	// The packet is not valid in the other direction.
	if _, _, err := PacketDecrypt(raw, sender, nil); err == nil {
		t.Fatal("packet accepted by its sender")
	}
}

// TestPacketBitFlip verifies that every single bit flip of a direct and of an embedded packet is rejected.
func TestPacketBitFlip(t *testing.T) {
	sender := generateKeyPair(t)
	receiver := generateKeyPair(t)
	relay := generateKeyPair(t)

	packet := &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 1, Payload: []byte("payload")}

	direct, err := PacketEncrypt(sender, receiver.PublicKey, packet, nil)
	if err != nil {
		t.Fatal(err)
	}
	for bit := 0; bit < len(direct)*8; bit++ {
		if _, _, err := PacketDecrypt(flipBit(direct, bit), receiver, nil); err == nil {
			t.Fatalf("direct packet with flipped bit %d accepted", bit)
		}
	}

	embedded, err := PacketEncryptEmbedded(sender, receiver.PublicKey, relay.PublicKey, packet)
	if err != nil {
		t.Fatal(err)
	}
	for bit := 0; bit < len(embedded)*8; bit++ {
		if _, _, err := PacketDecryptEmbedded(flipBit(embedded, bit), receiver, relay.PublicKey); err == nil {
			t.Fatalf("embedded packet with flipped bit %d accepted", bit)
		}
	}

	// every single bit flip of the relay Peer ID
	for bit := 0; bit < len(relay.PublicKey)*8; bit++ {
		if _, _, err := PacketDecryptEmbedded(embedded, receiver, flipBit(relay.PublicKey, bit)); err == nil {
			t.Fatalf("embedded packet with flipped bit %d of the relay Peer ID accepted", bit)
		}
	}
}

// TestPacketSenderSignBit verifies that a packet is rejected if the top bit of the sender's public key is flipped (byte 43, bit 7).
// The two Ed25519 public keys convert to the same X25519 public key and therefore to the same shared secret. The packet is rejected because both Peer IDs are part of the packet key.
func TestPacketSenderSignBit(t *testing.T) {
	sender := generateKeyPair(t)
	receiver := generateKeyPair(t)

	raw, err := PacketEncrypt(sender, receiver.PublicKey, &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 1, Payload: []byte("payload")}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Flipping the bit in the encrypted sender's public key flips the same bit in the decrypted one.
	flipped := flipBit(raw, 43*8+7)

	flippedSender, err := PacketEmbeddedSender(flipped, receiver.PublicKey)
	if err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(flippedSender, flipBit(sender.PublicKey, 31*8+7)) {
		t.Fatal("the packet does not carry the sender's public key with the top bit flipped")
	}

	// It is a valid Ed25519 public key with the same X25519 public key.
	point, err := new(edwards25519.Point).SetBytes(flippedSender)
	if err != nil {
		t.Fatalf("the sender's public key with the top bit flipped is not a valid public key: %v", err)
	} else if !bytes.Equal(point.BytesMontgomery(), sender.x25519.PublicKey().Bytes()) {
		t.Fatal("the sender's public key with the top bit flipped converts to another X25519 public key")
	}

	if _, _, err := PacketDecrypt(flipped, receiver, nil); err == nil || err.Error() != "invalid authentication tag" {
		t.Fatalf("packet with the top bit of the sender's public key flipped: %v, expected invalid authentication tag", err)
	}
}

// packetFromSender returns a packet of minimum size for the receiver that carries the sender's public key. The encrypted fields and the authentication tag are random.
func packetFromSender(t *testing.T, senderPublicKey, receiverPublicKey ed25519.PublicKey) (raw []byte) {
	raw = make([]byte, PacketLengthMin)
	rand.Read(raw)
	if err := packetSenderXOR(raw[packetNonceSize:packetOffsetEncrypted], senderPublicKey, raw[:packetNonceSize], receiverPublicKey); err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestPacketSenderInvalid verifies that a packet is rejected if the sender's public key is of low order, in which case the shared secret is all zero,
// and if it is not a valid Ed25519 public key. A packet to such a public key is not created.
func TestPacketSenderInvalid(t *testing.T) {
	sender := generateKeyPair(t)
	receiver := generateKeyPair(t)

	packet := &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 1}

	// The points of low order of the curve, in their canonical encoding.
	lowOrder := []string{
		"0100000000000000000000000000000000000000000000000000000000000000", // order 1
		"ecffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f", // order 2
		"0000000000000000000000000000000000000000000000000000000000000000", // order 4
		"0000000000000000000000000000000000000000000000000000000000000080", // order 4
		"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc05", // order 8
		"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc85", // order 8
		"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac037a", // order 8
		"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac03fa", // order 8
	}

	for _, keyHex := range lowOrder {
		key := ed25519.PublicKey(fromHex(t, keyHex))

		if point, err := new(edwards25519.Point).SetBytes(key); err != nil {
			t.Fatalf("%s is not a point of the curve: %v", keyHex, err)
		} else if point.MultByCofactor(point).Equal(edwards25519.NewIdentityPoint()) != 1 {
			t.Fatalf("%s is not a point of low order", keyHex)
		}

		if _, _, err := PacketDecrypt(packetFromSender(t, key, receiver.PublicKey), receiver, nil); err == nil || err.Error() != "invalid shared secret" {
			t.Errorf("packet from %s: %v, expected invalid shared secret", keyHex, err)
		}
		if _, err := PacketEncrypt(sender, key, packet, nil); err == nil || err.Error() != "invalid shared secret" {
			t.Errorf("packet to %s: %v, expected invalid shared secret", keyHex, err)
		}
	}

	// 32 bytes that are not a point of the curve
	invalid := make(ed25519.PublicKey, ed25519.PublicKeySize)
	for invalid[0] = 2; ; invalid[0]++ {
		if _, err := new(edwards25519.Point).SetBytes(invalid); err != nil {
			break
		} else if invalid[0] == 255 {
			t.Fatal("no invalid public key found")
		}
	}

	if _, _, err := PacketDecrypt(packetFromSender(t, invalid, receiver.PublicKey), receiver, nil); err == nil || err.Error() != "invalid public key" {
		t.Errorf("packet from %x: %v, expected invalid public key", invalid, err)
	}
	if _, err := PacketEncrypt(sender, invalid, packet, nil); err == nil || err.Error() != "invalid public key" {
		t.Errorf("packet to %x: %v, expected invalid public key", invalid, err)
	}
	if _, err := PacketEncrypt(sender, invalid[:31], packet, nil); err == nil {
		t.Error("packet to a public key of 31 bytes created")
	}

	// a valid sender's public key with random encrypted fields and authentication tag
	for n := 0; n < 1000; n++ {
		if _, _, err := PacketDecrypt(packetFromSender(t, sender.PublicKey, receiver.PublicKey), receiver, nil); err == nil || err.Error() != "invalid authentication tag" {
			t.Fatalf("random packet from a valid sender: %v, expected invalid authentication tag", err)
		}
	}

	// without key pair
	if _, err := PacketEncrypt(nil, receiver.PublicKey, packet, nil); err == nil {
		t.Error("packet created without key pair of the sender")
	}
	if _, _, err := PacketDecrypt(packetFromSender(t, sender.PublicKey, receiver.PublicKey), nil, nil); err == nil {
		t.Error("packet accepted without key pair of the receiver")
	}
	if _, err := NewKeyPair(nil); err == nil {
		t.Error("key pair created without private key")
	}
}

// TestPacketTruncated verifies that a packet of any size below the minimum is rejected, as well as a packet whose payload size field exceeds the packet.
func TestPacketTruncated(t *testing.T) {
	sender := generateKeyPair(t)
	receiver := generateKeyPair(t)
	relay := generateKeyPair(t)

	packet := &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 1}

	direct, err := packetEncrypt(sender, receiver.PublicKey, make([]byte, packetNonceSize), nil, packet, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := PacketEncryptEmbedded(sender, receiver.PublicKey, relay.PublicKey, packet)
	if err != nil {
		t.Fatal(err)
	}
	if len(direct) != PacketLengthMin || len(embedded) != PacketLengthMinEmbedded {
		t.Fatalf("packet without payload has %d bytes, embedded %d bytes", len(direct), len(embedded))
	}

	for n := 0; n < PacketLengthMin; n++ {
		if _, _, err := PacketDecrypt(direct[:n], receiver, nil); err == nil {
			t.Fatalf("direct packet of %d bytes accepted", n)
		}
		if _, _, err := PacketDecryptEmbedded(embedded[:n], receiver, relay.PublicKey); err == nil {
			t.Fatalf("embedded packet of %d bytes accepted", n)
		}
		if _, err := PacketEmbeddedSender(embedded[:n], receiver.PublicKey); err == nil {
			t.Fatalf("sender of an embedded packet of %d bytes returned", n)
		}
	}

	// The size of payload data is 8, the packet carries 7 bytes after the field. It is authenticated by the sender.
	key, err := sender.packetKeyCached(nil, receiver.PublicKey, true)
	if err != nil {
		t.Fatal(err)
	}
	exceed, err := packetEncrypt(sender, receiver.PublicKey, make([]byte, packetNonceSize), nil, &PacketRaw{Payload: make([]byte, 7)}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	exceed = packetSeal(t, key, exceed, func(fields []byte) { fields[6] = 8 }, nil)
	if _, _, err := PacketDecrypt(exceed, receiver, nil); err == nil || err.Error() != "invalid length field" {
		t.Fatalf("packet whose payload size exceeds the packet: %v, expected invalid length field", err)
	}
}

// packetSeal decrypts the fields of a packet with the packet key, lets the function change them, and encrypts and authenticates them again.
func packetSeal(t *testing.T, key, raw []byte, change func(fields []byte), additionalData []byte) (result []byte) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := aead.Open(nil, raw[:packetNonceSize], raw[packetOffsetEncrypted:], additionalData)
	if err != nil {
		t.Fatal(err)
	}
	change(fields)

	return aead.Seal(append([]byte{}, raw[:packetOffsetEncrypted]...), raw[:packetNonceSize], fields, additionalData)
}

// TestPacketEmbeddedInvalid verifies that an embedded packet is bound to its end receiver and its relay, that it is rejected as a direct packet,
// that a direct packet is rejected inside a Traverse message, with and without randomized garbage, and that an embedded packet with randomized garbage is rejected.
func TestPacketEmbeddedInvalid(t *testing.T) {
	sender := generateKeyPair(t)
	receiver := generateKeyPair(t)
	relay := generateKeyPair(t)
	other := generateKeyPair(t)

	packet := &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 1, Payload: []byte("test payload")}

	embedded, err := PacketEncryptEmbedded(sender, receiver.PublicKey, relay.PublicKey, packet)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := PacketDecryptEmbedded(embedded, receiver, relay.PublicKey); err != nil {
		t.Fatal(err)
	}

	// another end receiver
	if _, _, err := PacketDecryptEmbedded(embedded, other, relay.PublicKey); err == nil {
		t.Fatal("embedded packet for another end receiver accepted")
	}

	// a Traverse message delivered by a peer other than the relay it was built for
	if _, _, err := PacketDecryptEmbedded(embedded, receiver, other.PublicKey); err == nil || err.Error() != "invalid authentication tag" {
		t.Fatalf("embedded packet delivered by another relay: %v, expected invalid authentication tag", err)
	}

	// The embedded packet sent directly has the size of a direct packet without randomized garbage. Only the authentication tag rejects it.
	if _, _, err := PacketDecrypt(embedded, receiver, nil); err == nil || err.Error() != "invalid authentication tag" {
		t.Fatalf("embedded packet as direct packet: %v, expected invalid authentication tag", err)
	}

	// A direct packet without randomized garbage inside a Traverse message has the size of an embedded packet. Only the authentication tag rejects it.
	nonce := make([]byte, packetNonceSize)
	rand.Read(nonce)
	direct, err := packetEncrypt(sender, receiver.PublicKey, nonce, nil, packet, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := PacketDecrypt(direct, receiver, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PacketDecryptEmbedded(direct, receiver, relay.PublicKey); err == nil || err.Error() != "invalid authentication tag" {
		t.Fatalf("direct packet without garbage as embedded packet: %v, expected invalid authentication tag", err)
	}

	// direct packets as sent, with randomized garbage
	for n := 0; n < 100; n++ {
		direct, err := PacketEncrypt(sender, receiver.PublicKey, packet, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := PacketDecryptEmbedded(direct, receiver, relay.PublicKey); err == nil {
			t.Fatal("direct packet accepted as embedded packet")
		}
	}

	// randomized garbage after the payload, authenticated by the sender for the relay
	for sizeGarbage := 1; sizeGarbage < maxRandomGarbage; sizeGarbage++ {
		withGarbage, err := packetEncrypt(sender, receiver.PublicKey, nonce, embeddedAdditionalData(relay.PublicKey, CommandTraverse), packet, make([]byte, sizeGarbage), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, garbage, err := packetDecrypt(withGarbage, receiver, embeddedAdditionalData(relay.PublicKey, CommandTraverse), nil); err != nil || garbage != sizeGarbage {
			t.Fatalf("embedded packet with %d bytes garbage: %d bytes garbage, %v", sizeGarbage, garbage, err)
		}
		if _, _, err := PacketDecryptEmbedded(withGarbage, receiver, relay.PublicKey); err == nil || err.Error() != "embedded packet: invalid length" {
			t.Fatalf("embedded packet with %d bytes garbage: %v, expected invalid length", sizeGarbage, err)
		}
	}

	// The additional authenticated data includes the command number of the Traverse message: an embedded packet that is authenticated
	// with the Peer ID of the relay alone, or with the Peer ID of the relay followed by another command number, is rejected by the authentication tag.
	for _, additionalData := range [][]byte{relay.PublicKey, embeddedAdditionalData(relay.PublicKey, CommandTraverse+1)} {
		invalid, err := packetEncrypt(sender, receiver.PublicKey, nonce, additionalData, packet, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := packetDecrypt(invalid, receiver, additionalData, nil); err != nil {
			t.Fatal(err)
		}
		if _, _, err := PacketDecryptEmbedded(invalid, receiver, relay.PublicKey); err == nil || err.Error() != "invalid authentication tag" {
			t.Fatalf("embedded packet with %d bytes additional authenticated data: %v, expected invalid authentication tag", len(additionalData), err)
		}
	}

	// invalid relay Peer ID
	if _, _, err := PacketDecryptEmbedded(embedded, receiver, nil); err == nil {
		t.Fatal("embedded packet with empty relay Peer ID accepted")
	}
	if _, err := PacketEncryptEmbedded(sender, receiver.PublicKey, nil, packet); err == nil {
		t.Fatal("embedded packet encrypted with empty relay Peer ID")
	}

	// A direct packet has no additional authenticated data: one that is authenticated with empty additional data is not valid as embedded packet of a relay, and vice versa.
	if _, _, _, err := packetDecrypt(embedded, receiver, nil, nil); err == nil {
		t.Fatal("embedded packet accepted without additional authenticated data")
	}
}

// TestPacketUnique verifies that the same packet built twice differs in every part: nonce, encrypted sender's public key, encrypted fields and authentication tag.
func TestPacketUnique(t *testing.T) {
	sender := generateKeyPair(t)
	receiver := generateKeyPair(t)
	relay := generateKeyPair(t)

	packet := &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 1, Payload: []byte("test payload")}

	compare := func(name string, raw1, raw2 []byte) {
		parts := []struct {
			name   string
			a, b   []byte
			length int
		}{
			{"nonce", raw1[:packetNonceSize], raw2[:packetNonceSize], 12},
			{"encrypted sender's public key", raw1[packetNonceSize:packetOffsetEncrypted], raw2[packetNonceSize:packetOffsetEncrypted], 32},
			{"encrypted fields and payload", raw1[packetOffsetEncrypted : packetHeaderSize+len(packet.Payload)], raw2[packetOffsetEncrypted : packetHeaderSize+len(packet.Payload)], 8 + len(packet.Payload)},
			{"authentication tag", raw1[len(raw1)-packetTagSize:], raw2[len(raw2)-packetTagSize:], 16},
		}
		for _, part := range parts {
			if len(part.a) != part.length || len(part.b) != part.length {
				t.Fatalf("%s: %s has %d and %d bytes, expected %d", name, part.name, len(part.a), len(part.b), part.length)
			} else if bytes.Equal(part.a, part.b) {
				t.Errorf("%s: %s is the same in both packets", name, part.name)
			}
		}
	}

	direct1, err1 := PacketEncrypt(sender, receiver.PublicKey, packet, nil)
	direct2, err2 := PacketEncrypt(sender, receiver.PublicKey, packet, nil)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	compare("direct packet", direct1, direct2)

	embedded1, err1 := PacketEncryptEmbedded(sender, receiver.PublicKey, relay.PublicKey, packet)
	embedded2, err2 := PacketEncryptEmbedded(sender, receiver.PublicKey, relay.PublicKey, packet)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	compare("embedded packet", embedded1, embedded2)

	// A direct packet and the same packet embedded in a Traverse message have their own nonce.
	compare("direct and embedded packet", direct1, embedded1)
}

// TestTraverseLayout checks the payload layout: end receiver peer ID (32), addresses and ports (28), embedded packet.
func TestTraverseLayout(t *testing.T) {
	sender := generateKeyPair(t)
	relay := generateKeyPair(t)
	target := generateKeyPair(t)

	embedded, err := PacketEncryptEmbedded(sender, target.PublicKey, relay.PublicKey, &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 7, Payload: []byte("hello")})
	if err != nil {
		t.Fatal(err)
	}

	payload, err := EncodeTraverse(embedded, target.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	if len(payload) != 60+len(embedded) {
		t.Fatalf("payload size %d, expected %d", len(payload), 60+len(embedded))
	}
	if !bytes.Equal(payload[0:32], target.PublicKey) || !bytes.Equal(payload[32:60], make([]byte, 28)) || !bytes.Equal(payload[60:], embedded) {
		t.Fatal("end receiver, zero address block or embedded packet at wrong offset")
	}

	// the relay sets the addresses and ports of the original sender
	ipv4 := net.IP{203, 0, 113, 7}
	ipv6 := net.ParseIP("2001:db8::7")
	if err := EncodeTraverseSetAddress(payload, ipv4, 1001, 1002, ipv6, 1003, 1004); err != nil {
		t.Fatal(err)
	}

	expected := []byte{203, 0, 113, 7, 0xE9, 0x03, 0xEA, 0x03}
	expected = append(expected, ipv6...)
	expected = append(expected, 0xEB, 0x03, 0xEC, 0x03)
	if !bytes.Equal(payload[32:60], expected) {
		t.Fatalf("addresses and ports at offset 32 are %x, expected %x", payload[32:60], expected)
	}
	if !bytes.Equal(payload[0:32], target.PublicKey) || !bytes.Equal(payload[60:], embedded) {
		t.Fatal("setting the addresses changed the end receiver or the embedded packet")
	}

	msg, err := DecodeTraverse(&MessageRaw{PacketRaw: PacketRaw{Command: CommandTraverse, Payload: payload}, SenderPublicKey: relay.PublicKey})
	if err != nil {
		t.Fatal(err)
	}
	if !msg.TargetPeer.Equal(target.PublicKey) || !bytes.Equal(msg.EmbeddedPacketRaw, embedded) {
		t.Fatal("end receiver or embedded packet mismatch")
	}
	if !msg.IPv4.Equal(ipv4) || msg.PortIPv4 != 1001 || msg.PortIPv4ReportedExternal != 1002 || !msg.IPv6.Equal(ipv6) || msg.PortIPv6 != 1003 || msg.PortIPv6ReportedExternal != 1004 {
		t.Fatal("addresses or ports mismatch")
	}

	// the relay reads the original sender from the embedded packet
	if embeddedSender, err := PacketEmbeddedSender(msg.EmbeddedPacketRaw, msg.TargetPeer); err != nil || !embeddedSender.Equal(sender.PublicKey) {
		t.Fatalf("sender of the embedded packet read as %x: %v", embeddedSender, err)
	}

	// the embedded packet identifies the original sender
	decoded, embeddedSender, err := PacketDecryptEmbedded(msg.EmbeddedPacketRaw, target, relay.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if !embeddedSender.Equal(sender.PublicKey) || decoded.Command != CommandAnnouncement || decoded.Sequence != 7 || !bytes.Equal(decoded.Payload, []byte("hello")) {
		t.Fatal("embedded packet mismatch")
	}

	// a payload with an embedded packet below the minimum size is rejected
	short := 60 + PacketLengthMinEmbedded - 1
	if _, err := DecodeTraverse(&MessageRaw{PacketRaw: PacketRaw{Command: CommandTraverse, Payload: payload[:short]}}); err == nil {
		t.Fatalf("payload of %d bytes accepted", short)
	}
	if err := EncodeTraverseSetAddress(payload[:59], ipv4, 1001, 1002, ipv6, 1003, 1004); err == nil {
		t.Fatal("set address on payload of 59 bytes accepted")
	}
}

// TestPacketKeys checks the cache of the packet keys: the keys are derived and stored with the first packet and used for the following ones,
// the packets are the same as without the cache, and a packet that is not valid is rejected with the cached key as well.
func TestPacketKeys(t *testing.T) {
	sender := newKeyPair(t, ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32)))
	receiver := newKeyPair(t, ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, 32)))

	nonce := fromHex(t, "000102030405060708090a0b")
	packet := &PacketRaw{Protocol: 0, Command: 1, Sequence: 0x01020304, Payload: []byte("Peernet")}

	// The packet and the packet key of the test vector, see TestPacketVector.
	vectorDirect := fromHex(t, "000102030405060708090a0b973aaf461f9e9e39c748b95c1c9f40e79bf35d140d613746347134957ee92b34ee9cd13863e0a5fdb40c6b5e10a5c7017083798785b26c6d065bdb6d21d441")
	vectorPacketKey := fromHex(t, "f70ceef250387a6627662a74f7aa920ae5555e83171fdc9c0d5c6ac14e9b1ba0")

	keysSender, keysReceiver := &PacketKeys{}, &PacketKeys{} // cache at the sender for the receiver, and at the receiver for the sender

	// Without and with the keys in the cache the packet is the one of the test vector.
	for n := 0; n < 3; n++ {
		raw, err := packetEncrypt(sender, receiver.PublicKey, nonce, nil, packet, nil, keysSender)
		if err != nil {
			t.Fatal(err)
		} else if !bytes.Equal(raw, vectorDirect) {
			t.Fatalf("packet %d with cache is\n%x, expected\n%x", n, raw, vectorDirect)
		}

		decoded, senderPublicKey, err := PacketDecrypt(raw, receiver, keysReceiver)
		if err != nil {
			t.Fatalf("packet %d with cache: %v", n, err)
		} else if !senderPublicKey.Equal(sender.PublicKey) || decoded.Command != 1 || decoded.Sequence != 0x01020304 || string(decoded.Payload) != "Peernet" {
			t.Fatalf("packet %d with cache decoded as %+v from %x", n, decoded, senderPublicKey)
		}
	}

	// Each side stored both keys for the public key of the other. The two directions have different keys, and both sides hold the same two.
	if !bytes.Equal(keysSender.publicKey, receiver.PublicKey) || !bytes.Equal(keysSender.send, vectorPacketKey) {
		t.Fatal("the sender did not store the packet key for sending")
	}
	if !bytes.Equal(keysReceiver.publicKey, sender.PublicKey) || !bytes.Equal(keysReceiver.receive, vectorPacketKey) {
		t.Fatal("the receiver did not store the packet key for receiving")
	}
	if len(keysSender.receive) != 32 || bytes.Equal(keysSender.send, keysSender.receive) {
		t.Error("the two directions use the same packet key")
	}
	if !bytes.Equal(keysSender.receive, keysReceiver.send) {
		t.Error("the two sides hold different packet keys")
	}

	// A packet of the other direction is exchanged with the cached keys.
	reply, err := PacketEncrypt(receiver, sender.PublicKey, packet, keysReceiver)
	if err != nil {
		t.Fatal(err)
	}
	if _, senderPublicKey, err := PacketDecrypt(reply, sender, keysSender); err != nil || !senderPublicKey.Equal(receiver.PublicKey) {
		t.Fatalf("packet of the other direction: %v", err)
	}
	if _, senderPublicKey, err := PacketDecrypt(reply, sender, nil); err != nil || !senderPublicKey.Equal(receiver.PublicKey) {
		t.Fatalf("packet of the other direction without cache: %v", err)
	}

	// The cached key is the one that is used: with another key in the cache the packet is not readable by the receiver, and the receiver does not read a valid packet.
	otherKey := bytes.Repeat([]byte{7}, 32)
	if raw, err := PacketEncrypt(sender, receiver.PublicKey, packet, &PacketKeys{publicKey: receiver.PublicKey, send: otherKey, receive: otherKey}); err != nil {
		t.Fatal(err)
	} else if _, _, err := PacketDecrypt(raw, receiver, nil); err == nil {
		t.Error("the cached packet key was not used for sending")
	}
	if _, _, err := PacketDecrypt(vectorDirect, receiver, &PacketKeys{publicKey: sender.PublicKey, send: otherKey, receive: otherKey}); err == nil {
		t.Error("the cached packet key was not used for receiving")
	}

	// With the key cached, every single bit flip is rejected, as is a packet for another receiver.
	for bit := 0; bit < len(vectorDirect)*8; bit++ {
		if _, _, err := PacketDecrypt(flipBit(vectorDirect, bit), receiver, keysReceiver); err == nil {
			t.Fatalf("packet with flipped bit %d accepted with the cached key", bit)
		}
	}
	other := generateKeyPair(t)
	if raw, err := PacketEncrypt(sender, other.PublicKey, packet, nil); err != nil {
		t.Fatal(err)
	} else if _, _, err := PacketDecrypt(raw, receiver, keysReceiver); err == nil {
		t.Error("packet for another receiver accepted with the cached key")
	}

	// The cache of one peer is not used for another peer. A packet of the sender that carries the public key of another peer is rejected with the cache of the sender:
	// the packet key is derived for the public key in the packet.
	forged := append([]byte{}, vectorDirect...)
	if err := packetSenderXOR(forged[packetNonceSize:packetOffsetEncrypted], other.PublicKey, forged[:packetNonceSize], receiver.PublicKey); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PacketDecrypt(forged, receiver, keysReceiver); err == nil || err.Error() != "invalid authentication tag" {
		t.Errorf("packet of the sender with the public key of another peer, with the cache of the sender: %v, expected invalid authentication tag", err)
	}
	if raw, err := PacketEncrypt(sender, other.PublicKey, packet, keysSender); err != nil {
		t.Fatal(err)
	} else if _, senderPublicKey, err := PacketDecrypt(raw, other, nil); err != nil || !senderPublicKey.Equal(sender.PublicKey) {
		t.Errorf("packet to another peer with the cache of the receiver: %v", err)
	}
	if !bytes.Equal(keysReceiver.publicKey, sender.PublicKey) || !bytes.Equal(keysReceiver.receive, vectorPacketKey) {
		t.Error("the cache was replaced by a packet with another public key")
	}
	if !bytes.Equal(keysSender.publicKey, receiver.PublicKey) || !bytes.Equal(keysSender.send, vectorPacketKey) {
		t.Error("the cache was replaced by a packet to another public key")
	}

	// Keys that cannot be derived are not stored.
	keysInvalid := &PacketKeys{}
	lowOrder := ed25519.PublicKey(fromHex(t, "0100000000000000000000000000000000000000000000000000000000000000"))
	if _, err := PacketEncrypt(sender, lowOrder, packet, keysInvalid); err == nil {
		t.Error("packet to a public key of low order created")
	}
	if _, _, err := PacketDecrypt(packetFromSender(t, lowOrder, receiver.PublicKey), receiver, keysInvalid); err == nil {
		t.Error("packet from a public key of low order accepted")
	}
	if keysInvalid.publicKey != nil || keysInvalid.send != nil || keysInvalid.receive != nil {
		t.Error("packet keys were stored for a public key of low order")
	}

	// concurrent use of an empty cache
	keysSender, keysReceiver = &PacketKeys{}, &PacketKeys{}
	var wg sync.WaitGroup
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := 0; m < 20; m++ {
				raw, err := PacketEncrypt(sender, receiver.PublicKey, packet, keysSender)
				if err != nil {
					t.Error(err)
					return
				}
				if _, senderPublicKey, err := PacketDecrypt(raw, receiver, keysReceiver); err != nil || !senderPublicKey.Equal(sender.PublicKey) {
					t.Errorf("concurrent use: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if !bytes.Equal(keysSender.send, vectorPacketKey) || !bytes.Equal(keysReceiver.receive, vectorPacketKey) {
		t.Error("concurrent use: another packet key is stored")
	}

	// PacketSender returns the sender for the cache lookup and rejects every size below the minimum.
	if senderPublicKey, err := PacketSender(vectorDirect, receiver.PublicKey); err != nil || !senderPublicKey.Equal(sender.PublicKey) {
		t.Errorf("sender of the packet read as %x: %v", senderPublicKey, err)
	}
	for n := 0; n < PacketLengthMin; n++ {
		if _, err := PacketSender(vectorDirect[:n], receiver.PublicKey); err == nil {
			t.Fatalf("sender of a packet of %d bytes returned", n)
		}
	}
}
