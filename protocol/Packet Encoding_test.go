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
	"encoding/binary"
	"encoding/hex"
	"net"
	"testing"

	"golang.org/x/crypto/salsa20"
)

func generateKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return publicKey, privateKey
}

// testSalsa20Pass applies one Salsa20 pass to a packet as the packet structure defines it, without using the packet code:
// key = Peer ID, nonce = the 8 bytes at len-64, one key stream from offset 0, the nonce itself stays as it is.
// It turns an encrypted packet into plaintext and the reverse.
func testSalsa20Pass(raw []byte, peerID ed25519.PublicKey) (result []byte) {
	var key [32]byte
	copy(key[:], peerID)
	nonce := raw[len(raw)-64 : len(raw)-56]

	result = make([]byte, len(raw))
	salsa20.XORKeyStream(result, raw, nonce, &key)
	copy(result[len(raw)-64:], nonce)

	return result
}

// TestPacketRoundTrip encrypts a packet and verifies that the receiver decodes the same fields and identifies the sender from the packet.
func TestPacketRoundTrip(t *testing.T) {
	senderPublic, senderPrivate := generateKey(t)
	receiverPublic, _ := generateKey(t)

	packet := &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 12345, Payload: []byte("payload")}

	raw, err := PacketEncrypt(senderPrivate, receiverPublic, packet)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < PacketLengthMin+len(packet.Payload) {
		t.Fatalf("packet too short: %d", len(raw))
	}

	decoded, sender, err := PacketDecrypt(raw, receiverPublic)
	if err != nil {
		t.Fatal(err)
	}
	if !sender.Equal(senderPublic) {
		t.Fatal("sender public key mismatch")
	}
	if decoded.Protocol != packet.Protocol || decoded.Command != packet.Command || decoded.Sequence != packet.Sequence || !bytes.Equal(decoded.Payload, packet.Payload) {
		t.Fatalf("decoded packet mismatch: %+v", decoded)
	}
}

// TestPacketSize verifies the packet sizes: 104 bytes for the smallest packet, and the size of the payload and of the randomized garbage on top.
func TestPacketSize(t *testing.T) {
	_, senderPrivate := generateKey(t)
	receiverPublic, _ := generateKey(t)
	relayPublic, _ := generateKey(t)

	if PacketLengthMin != 104 {
		t.Fatalf("minimum packet size %d, expected 104", PacketLengthMin)
	}

	// direct packet without payload and without garbage
	if raw := packetEncrypt(senderPrivate, receiverPublic, receiverPublic, &PacketRaw{Command: CommandPing}, nil); len(raw) != 104 {
		t.Fatalf("smallest direct packet is %d bytes, expected 104", len(raw))
	}

	for size := 0; size < 300; size++ {
		packet := &PacketRaw{Command: CommandAnnouncement, Payload: make([]byte, size)}

		raw, err := PacketEncrypt(senderPrivate, receiverPublic, packet)
		if err != nil {
			t.Fatal(err)
		} else if len(raw) < 104+size || len(raw) >= 104+size+maxRandomGarbage {
			t.Fatalf("direct packet with payload of %d bytes is %d bytes", size, len(raw))
		}

		embedded, err := PacketEncryptEmbedded(senderPrivate, receiverPublic, relayPublic, packet)
		if err != nil {
			t.Fatal(err)
		} else if len(embedded) != 104+size {
			t.Fatalf("embedded packet with payload of %d bytes is %d bytes, expected %d", size, len(embedded), 104+size)
		}
	}
}

// TestPacketLayout checks the bytes of a direct packet against the packet structure without using the decoder:
// The 8 bytes at len-64 are the first 8 bytes of the signature over the receiver's Peer ID followed by the plaintext fields,
// and one Salsa20 pass from offset 0 with these 8 bytes as nonce returns the fields at their offsets and the remaining 56 bytes of the signature.
func TestPacketLayout(t *testing.T) {
	senderPublic, senderPrivate := generateKey(t)
	receiverPublic, _ := generateKey(t)

	packet := &PacketRaw{Protocol: ProtocolVersion, Command: CommandResponse, Sequence: 0x01020304, Payload: []byte("test payload")}

	check := func(raw []byte, sizeGarbage int) {
		if len(raw) != 40+len(packet.Payload)+sizeGarbage+64 {
			t.Fatalf("packet size %d with %d bytes garbage", len(raw), sizeGarbage)
		}

		plain := testSalsa20Pass(raw, receiverPublic)
		fields, signature := plain[:len(plain)-64], plain[len(plain)-64:]

		if fields[0] != ProtocolVersion || fields[1] != CommandResponse || binary.LittleEndian.Uint32(fields[2:6]) != 0x01020304 {
			t.Fatalf("version, command or sequence at wrong offset: %x", fields[0:6])
		}
		if !bytes.Equal(fields[6:38], senderPublic) {
			t.Fatal("sender's public key not at offset 6")
		}
		if int(binary.LittleEndian.Uint16(fields[38:40])) != len(packet.Payload) || !bytes.Equal(fields[40:40+len(packet.Payload)], packet.Payload) {
			t.Fatal("payload size not at offset 38 or payload not at offset 40")
		}

		signed := append(append([]byte{}, receiverPublic...), fields...)
		if !bytes.Equal(signature, ed25519.Sign(senderPrivate, signed)) {
			t.Fatal("signature is not the one over the receiver's Peer ID followed by the plaintext fields")
		}
		if !bytes.Equal(raw[len(raw)-64:len(raw)-56], signature[:8]) {
			t.Fatal("first 8 bytes of the signature are not transmitted as they are")
		}

		// everything but the nonce is encrypted
		if bytes.Equal(raw[:40], fields[:40]) || bytes.Equal(raw[len(raw)-56:], signature[8:]) {
			t.Fatal("header or remaining signature bytes are not encrypted")
		}
	}

	for sizeGarbage := 0; sizeGarbage < maxRandomGarbage; sizeGarbage++ {
		garbage := make([]byte, sizeGarbage)
		rand.Read(garbage)
		check(packetEncrypt(senderPrivate, receiverPublic, receiverPublic, packet, garbage), sizeGarbage)
	}

	// the same through the exported function, which chooses the garbage
	for n := 0; n < 100; n++ {
		raw, err := PacketEncrypt(senderPrivate, receiverPublic, packet)
		if err != nil {
			t.Fatal(err)
		}
		check(raw, len(raw)-40-len(packet.Payload)-64)
	}
}

// TestPacketTampered verifies that any modified byte, including the nonce, and a wrong receiver are rejected.
func TestPacketTampered(t *testing.T) {
	_, senderPrivate := generateKey(t)
	receiverPublic, _ := generateKey(t)
	otherPublic, _ := generateKey(t)

	packet := &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 1, Payload: []byte("payload")}

	raw, err := PacketEncrypt(senderPrivate, receiverPublic, packet)
	if err != nil {
		t.Fatal(err)
	}

	// wrong receiver key
	if _, _, err := PacketDecrypt(raw, otherPublic); err == nil {
		t.Fatal("packet decrypted with wrong receiver key")
	}

	// flip every single byte once
	for n := range raw {
		tampered := make([]byte, len(raw))
		copy(tampered, raw)
		tampered[n] ^= 1

		if _, _, err := PacketDecrypt(tampered, receiverPublic); err == nil {
			t.Fatalf("tampered byte %d accepted", n)
		}
	}
}

// TestPacketReceiverBinding verifies that a packet is valid for one receiver only: another peer rejects it as it is,
// and also after it was decrypted with the receiver's Peer ID and encrypted again with the other peer's Peer ID. The signature covers the receiver's Peer ID.
func TestPacketReceiverBinding(t *testing.T) {
	_, senderPrivate := generateKey(t)
	receiverPublic, _ := generateKey(t)
	otherPublic, _ := generateKey(t)

	packet := &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 1, Payload: []byte("payload")}

	// without and with garbage
	for sizeGarbage := 0; sizeGarbage < maxRandomGarbage; sizeGarbage++ {
		raw := packetEncrypt(senderPrivate, receiverPublic, receiverPublic, packet, make([]byte, sizeGarbage))
		if _, _, err := PacketDecrypt(raw, receiverPublic); err != nil {
			t.Fatal(err)
		}

		if _, _, err := PacketDecrypt(raw, otherPublic); err == nil {
			t.Fatal("packet accepted by another receiver")
		}

		// Decrypted with the receiver's Peer ID and encrypted with the other peer's Peer ID, same nonce. The other peer decrypts it to the same plaintext.
		forwarded := testSalsa20Pass(testSalsa20Pass(raw, receiverPublic), otherPublic)
		if !bytes.Equal(testSalsa20Pass(forwarded, otherPublic), testSalsa20Pass(raw, receiverPublic)) {
			t.Fatal("re-encrypted packet does not decrypt to the same plaintext")
		}
		if _, _, err := PacketDecrypt(forwarded, otherPublic); err == nil {
			t.Fatal("packet re-encrypted for another receiver accepted")
		}
	}
}

// TestPacketEmbeddedRoundTrip encrypts and decrypts a packet embedded in a Traverse message and verifies the sender.
func TestPacketEmbeddedRoundTrip(t *testing.T) {
	senderPublic, senderPrivate := generateKey(t)
	receiverPublic, _ := generateKey(t)
	relayPublic, _ := generateKey(t)

	packet := &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 123456, Payload: []byte("test payload")}

	raw, err := PacketEncryptEmbedded(senderPrivate, receiverPublic, relayPublic, packet)
	if err != nil {
		t.Fatal(err)
	}

	// The embedded packet has no randomized garbage: 40 bytes header + payload + 64 bytes signature.
	if len(raw) != 40+len(packet.Payload)+64 {
		t.Fatalf("embedded packet size %d, expected %d", len(raw), 40+len(packet.Payload)+64)
	}

	decoded, sender, err := PacketDecryptEmbedded(raw, receiverPublic, relayPublic)
	if err != nil {
		t.Fatal(err)
	}
	if !sender.Equal(senderPublic) {
		t.Fatal("sender public key mismatch")
	}
	if decoded.Protocol != packet.Protocol || decoded.Command != packet.Command || decoded.Sequence != packet.Sequence || !bytes.Equal(decoded.Payload, packet.Payload) {
		t.Fatalf("packet mismatch: %+v", decoded)
	}

	// The signature covers the Peer ID of the end receiver, the Peer ID of the relay and the plaintext fields.
	plain := testSalsa20Pass(raw, receiverPublic)
	signed := append(append(append([]byte{}, receiverPublic...), relayPublic...), plain[:len(plain)-64]...)
	if !bytes.Equal(plain[len(plain)-64:], ed25519.Sign(senderPrivate, signed)) {
		t.Fatal("signature is not the one over the end receiver's Peer ID, the relay's Peer ID and the plaintext fields")
	}
}

// TestPacketEmbeddedInvalid verifies that an embedded packet is bound to its end receiver and its relay, and that tampered and truncated embedded packets and those with garbage are rejected.
func TestPacketEmbeddedInvalid(t *testing.T) {
	_, senderPrivate := generateKey(t)
	receiverPublic, _ := generateKey(t)
	relayPublic, _ := generateKey(t)
	otherPublic, _ := generateKey(t)

	packet := &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 1, Payload: []byte("test payload")}

	raw, err := PacketEncryptEmbedded(senderPrivate, receiverPublic, relayPublic, packet)
	if err != nil {
		t.Fatal(err)
	}

	// another end receiver, as it is and re-encrypted for it
	if _, _, err := PacketDecryptEmbedded(raw, otherPublic, relayPublic); err == nil {
		t.Fatal("embedded packet for another end receiver accepted")
	}
	if _, _, err := PacketDecryptEmbedded(testSalsa20Pass(testSalsa20Pass(raw, receiverPublic), otherPublic), otherPublic, relayPublic); err == nil {
		t.Fatal("embedded packet re-encrypted for another end receiver accepted")
	}

	// A Traverse delivered by a peer other than the relay it was built for: the end receiver verifies the signature with that peer's Peer ID.
	if _, _, err := PacketDecryptEmbedded(raw, receiverPublic, otherPublic); err == nil {
		t.Fatal("embedded packet delivered by another relay accepted")
	}

	// tampering with any single byte must invalidate the signature
	for n := range raw {
		tampered := make([]byte, len(raw))
		copy(tampered, raw)
		tampered[n] ^= 0x01

		if _, _, err := PacketDecryptEmbedded(tampered, receiverPublic, relayPublic); err == nil {
			t.Fatalf("tampered byte %d accepted", n)
		}
	}

	// truncated to any size below the minimum, including empty
	for n := 0; n < PacketLengthMin; n++ {
		if _, _, err := PacketDecryptEmbedded(raw[:n], receiverPublic, relayPublic); err == nil {
			t.Fatalf("embedded packet of %d bytes accepted", n)
		}
	}

	// truncated to any size below its own
	for n := PacketLengthMin; n < len(raw); n++ {
		if _, _, err := PacketDecryptEmbedded(raw[:n], receiverPublic, relayPublic); err == nil {
			t.Fatalf("embedded packet truncated to %d bytes accepted", n)
		}
	}

	// randomized garbage after the payload, validly signed
	signedPrefix := signedPrefixEmbedded(receiverPublic, relayPublic)
	for n := 1; n < maxRandomGarbage; n++ {
		withGarbage := packetEncrypt(senderPrivate, receiverPublic, signedPrefix, packet, make([]byte, n))
		if _, _, err := packetDecrypt(withGarbage, receiverPublic, signedPrefix); err != nil {
			t.Fatalf("embedded packet with %d bytes garbage: %v", n, err)
		}
		if _, _, err := PacketDecryptEmbedded(withGarbage, receiverPublic, relayPublic); err == nil {
			t.Fatalf("embedded packet with %d bytes garbage accepted", n)
		}
	}

	// invalid relay Peer ID
	if _, _, err := PacketDecryptEmbedded(raw, receiverPublic, nil); err == nil {
		t.Fatal("embedded packet with empty relay Peer ID accepted")
	}
	if _, err := PacketEncryptEmbedded(senderPrivate, receiverPublic, nil, packet); err == nil {
		t.Fatal("embedded packet encrypted with empty relay Peer ID")
	}
}

// TestPacketEmbeddedNotDirect verifies that an embedded packet taken out of a Traverse message is rejected as a direct packet,
// and that a direct packet placed into a Traverse message is rejected as embedded packet.
// Both have the same structure and are encrypted the same way. What differs is the signed data: the signature of the embedded packet also covers the relay's Peer ID.
func TestPacketEmbeddedNotDirect(t *testing.T) {
	_, senderPrivate := generateKey(t)
	receiverPublic, _ := generateKey(t)
	relayPublic, _ := generateKey(t)

	packet := &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 1, Payload: []byte("test payload")}

	// embedded packet sent directly
	embedded, err := PacketEncryptEmbedded(senderPrivate, receiverPublic, relayPublic, packet)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := PacketDecryptEmbedded(embedded, receiverPublic, relayPublic); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PacketDecrypt(embedded, receiverPublic); err == nil {
		t.Fatal("embedded packet accepted as direct packet")
	}

	// Direct packet without garbage placed into a Traverse message. It has the exact length of an embedded packet, so only the signature rejects it.
	direct := packetEncrypt(senderPrivate, receiverPublic, receiverPublic, packet, nil)
	if _, _, err := PacketDecrypt(direct, receiverPublic); err != nil {
		t.Fatal(err)
	}
	if len(direct) != len(embedded) {
		t.Fatalf("direct packet without garbage is %d bytes, embedded packet %d", len(direct), len(embedded))
	}
	if _, _, err := PacketDecryptEmbedded(direct, receiverPublic, relayPublic); err == nil || err.Error() != "invalid signature" {
		t.Fatalf("direct packet without garbage as embedded packet: %v, expected invalid signature", err)
	}

	// the relay being the receiver or the sender does not make it valid either
	if _, _, err := PacketDecryptEmbedded(direct, receiverPublic, receiverPublic); err == nil {
		t.Fatal("direct packet accepted as embedded packet with the receiver as relay")
	}
	if _, _, err := PacketDecryptEmbedded(direct, receiverPublic, senderPrivate.Public().(ed25519.PublicKey)); err == nil {
		t.Fatal("direct packet accepted as embedded packet with the sender as relay")
	}

	// direct packets as sent, with garbage
	for n := 0; n < 1000; n++ {
		direct, err := PacketEncrypt(senderPrivate, receiverPublic, packet)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := PacketDecryptEmbedded(direct, receiverPublic, relayPublic); err == nil {
			t.Fatal("direct packet accepted as embedded packet")
		}
	}
}

// TestPacketDeterministic verifies that the nonce follows from the packet: the same embedded packet encrypted twice is identical, and another sequence number gives another nonce.
func TestPacketDeterministic(t *testing.T) {
	_, senderPrivate := generateKey(t)
	receiverPublic, _ := generateKey(t)
	relayPublic, _ := generateKey(t)

	encrypt := func(sequence uint32) (raw []byte) {
		raw, err := PacketEncryptEmbedded(senderPrivate, receiverPublic, relayPublic, &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: sequence, Payload: []byte("test payload")})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	raw1, raw2, raw3 := encrypt(1), encrypt(1), encrypt(2)

	if !bytes.Equal(raw1, raw2) {
		t.Fatal("the same embedded packet encrypted twice differs")
	}
	if bytes.Equal(raw1[len(raw1)-64:len(raw1)-56], raw3[len(raw3)-64:len(raw3)-56]) {
		t.Fatal("same nonce for another sequence number")
	}

	// A field that is equal in both packets does not repeat on the wire: the sender's public key at offset 6.
	if bytes.Equal(raw1[6:38], raw3[6:38]) {
		t.Fatal("the sender's public key is equal on the wire in two packets")
	}
}

// TestPacketVectors decodes two packets that were created by an implementation independent of this one, and verifies that encoding the same input returns the same bytes.
// The keys are Ed25519 keys from seeds of 32 equal bytes: sender 0x01, receiver 0x02, relay 0x03. Neither packet has garbage.
func TestPacketVectors(t *testing.T) {
	key := func(seed byte, expectedPublic string) (ed25519.PublicKey, ed25519.PrivateKey) {
		privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
		publicKey := privateKey.Public().(ed25519.PublicKey)
		if hex.EncodeToString(publicKey) != expectedPublic {
			t.Fatalf("public key of seed %#x is %x, expected %s", seed, publicKey, expectedPublic)
		}
		return publicKey, privateKey
	}

	senderPublic, senderPrivate := key(0x01, "8a88e3dd7409f195fd52db2d3cba5d72ca6709bf1d94121bf3748801b40f6f5c")
	receiverPublic, _ := key(0x02, "8139770ea87d175f56a35466c34c7ecccb8d8a91b4ee37a25df60f5b8fc9b394")
	relayPublic, _ := key(0x03, "ed4928c628d1c2c6eae90338905995612959273a5c63f93636c14614ac8737d1")

	// direct packet: Ping, no payload, 104 bytes
	direct, _ := hex.DecodeString("9dd711d7e19e213e40edd01fe43970b1badc03eb9315f63c1432151ce8b6810974b126fea701f9dfb64b421620155de95d47bfb751372c18bff8cdb5a62f751674d1de160fe0ae1d123d45a526f6b1986babea6354a0708ed24f70a45f38a299121482fa1aa043d8")
	packetDirect := &PacketRaw{Protocol: ProtocolVersion, Command: CommandPing, Sequence: 0x01020304}

	if len(direct) != 104 {
		t.Fatalf("direct vector is %d bytes", len(direct))
	}
	decoded, sender, err := PacketDecrypt(direct, receiverPublic)
	if err != nil {
		t.Fatalf("direct vector: %v", err)
	}
	if !sender.Equal(senderPublic) || decoded.Protocol != packetDirect.Protocol || decoded.Command != packetDirect.Command || decoded.Sequence != packetDirect.Sequence || len(decoded.Payload) != 0 {
		t.Fatalf("direct vector decoded as %+v from %x", decoded, sender)
	}
	if raw := packetEncrypt(senderPrivate, receiverPublic, receiverPublic, packetDirect, nil); !bytes.Equal(raw, direct) {
		t.Fatalf("direct packet encoded as\n%x, expected\n%x", raw, direct)
	}

	// embedded packet via the relay: Announcement, payload "hello", 109 bytes
	embedded, _ := hex.DecodeString("4f874d99ea95602c4c6145e365d263e96c0b5b920aaa6b311037a3878e7acf193ca2d29a2d36a8e1e9f463fd0b311806227ba56e5d9083126f53eeb01fbf8b0080a9f0cb4955e0a5d62d05c849a0ab60a9dd09fb7b2d73c3de1c61888238ea8464a5e8a8977c218b1266d25471")
	packetEmbedded := &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 0x01020304, Payload: []byte("hello")}

	if len(embedded) != 109 {
		t.Fatalf("embedded vector is %d bytes", len(embedded))
	}
	decoded, sender, err = PacketDecryptEmbedded(embedded, receiverPublic, relayPublic)
	if err != nil {
		t.Fatalf("embedded vector: %v", err)
	}
	if !sender.Equal(senderPublic) || decoded.Protocol != packetEmbedded.Protocol || decoded.Command != packetEmbedded.Command || decoded.Sequence != packetEmbedded.Sequence || !bytes.Equal(decoded.Payload, packetEmbedded.Payload) {
		t.Fatalf("embedded vector decoded as %+v from %x", decoded, sender)
	}
	if raw, err := PacketEncryptEmbedded(senderPrivate, receiverPublic, relayPublic, packetEmbedded); err != nil || !bytes.Equal(raw, embedded) {
		t.Fatalf("embedded packet encoded as\n%x, expected\n%x (%v)", raw, embedded, err)
	}
}

// TestTraverseLayout checks the payload layout: end receiver peer ID (32), addresses and ports (28), embedded packet.
func TestTraverseLayout(t *testing.T) {
	senderPublic, senderPrivate := generateKey(t)
	relayPublic, _ := generateKey(t)
	targetPublic, _ := generateKey(t)

	embedded, err := PacketEncryptEmbedded(senderPrivate, targetPublic, relayPublic, &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 7, Payload: []byte("hello")})
	if err != nil {
		t.Fatal(err)
	}

	payload, err := EncodeTraverse(embedded, targetPublic)
	if err != nil {
		t.Fatal(err)
	}

	if len(payload) != 60+len(embedded) {
		t.Fatalf("payload size %d, expected %d", len(payload), 60+len(embedded))
	}
	if !bytes.Equal(payload[0:32], targetPublic) || !bytes.Equal(payload[32:60], make([]byte, 28)) || !bytes.Equal(payload[60:], embedded) {
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
	if !bytes.Equal(payload[0:32], targetPublic) || !bytes.Equal(payload[60:], embedded) {
		t.Fatal("setting the addresses changed the end receiver or the embedded packet")
	}

	msg, err := DecodeTraverse(&MessageRaw{PacketRaw: PacketRaw{Command: CommandTraverse, Payload: payload}, SenderPublicKey: relayPublic})
	if err != nil {
		t.Fatal(err)
	}
	if !msg.TargetPeer.Equal(targetPublic) || !bytes.Equal(msg.EmbeddedPacketRaw, embedded) {
		t.Fatal("end receiver or embedded packet mismatch")
	}
	if !msg.IPv4.Equal(ipv4) || msg.PortIPv4 != 1001 || msg.PortIPv4ReportedExternal != 1002 || !msg.IPv6.Equal(ipv6) || msg.PortIPv6 != 1003 || msg.PortIPv6ReportedExternal != 1004 {
		t.Fatal("addresses or ports mismatch")
	}

	// the embedded packet identifies the original sender
	decoded, embeddedSender, err := PacketDecryptEmbedded(msg.EmbeddedPacketRaw, targetPublic, relayPublic)
	if err != nil {
		t.Fatal(err)
	}
	if !embeddedSender.Equal(senderPublic) || decoded.Command != CommandAnnouncement || decoded.Sequence != 7 || !bytes.Equal(decoded.Payload, []byte("hello")) {
		t.Fatal("embedded packet mismatch")
	}

	// a payload with an embedded packet below the minimum size is rejected
	short := 60 + PacketLengthMin - 1
	if _, err := DecodeTraverse(&MessageRaw{PacketRaw: PacketRaw{Command: CommandTraverse, Payload: payload[:short]}}); err == nil {
		t.Fatalf("payload of %d bytes accepted", short)
	}
	if err := EncodeTraverseSetAddress(payload[:59], ipv4, 1001, 1002, ipv6, 1003, 1004); err == nil {
		t.Fatal("set address on payload of 59 bytes accepted")
	}
}
