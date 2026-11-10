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
	"net"
	"testing"
)

func generateKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return publicKey, privateKey
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

// TestPacketTampered verifies that any modified byte, a wrong receiver, and a forged sender key are rejected.
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

	// The embedded packet has no nonce field and no randomized garbage: 40 bytes header + payload + 64 bytes signature.
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

	// another end receiver
	if _, _, err := PacketDecryptEmbedded(raw, otherPublic, relayPublic); err == nil {
		t.Fatal("embedded packet for another end receiver accepted")
	}

	// A Traverse delivered by a peer other than the relay it was built for: the end receiver takes the nonce from that peer's Peer ID.
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
	for n := 0; n < PacketLengthMinEmbedded; n++ {
		if _, _, err := PacketDecryptEmbedded(raw[:n], receiverPublic, relayPublic); err == nil {
			t.Fatalf("embedded packet of %d bytes accepted", n)
		}
	}

	// randomized garbage after the payload, validly signed
	for n := 1; n < maxRandomGarbage; n++ {
		withGarbage := packetEncrypt(senderPrivate, receiverPublic, nil, relayPublic[:8], packet, make([]byte, n))
		if _, _, err := packetDecrypt(withGarbage, 0, relayPublic[:8], receiverPublic); err != nil {
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

// TestPacketEmbeddedNotDirect verifies that an embedded packet taken out of a Traverse message is rejected as a direct packet, with any nonce in front,
// and that a direct packet placed into a Traverse message is rejected as embedded packet.
// The worst case is tested too: a nonce that produces the same Salsa20 key stream for both. The signed bytes still differ (the direct packet's signature covers the nonce field).
func TestPacketEmbeddedNotDirect(t *testing.T) {
	_, senderPrivate := generateKey(t)
	receiverPublic, _ := generateKey(t)

	packet := &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 1, Payload: []byte("test payload")}

	// Relay Peer IDs as nonce source: a regular one, and one whose first 4 bytes repeat. The direct nonce (the same 4 bytes twice) then equals the embedded nonce.
	relayPublic, _ := generateKey(t)
	relayRepeat := make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(relayRepeat, relayPublic)
	copy(relayRepeat[4:8], relayRepeat[0:4])

	for _, relay := range []ed25519.PublicKey{relayPublic, relayRepeat} {
		embedded, err := PacketEncryptEmbedded(senderPrivate, receiverPublic, relay, packet)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := PacketDecryptEmbedded(embedded, receiverPublic, relay); err != nil {
			t.Fatal(err)
		}

		// embedded packet sent directly, with a nonce in front: first and second half of the relay's nonce, zero, and random ones
		nonces := [][]byte{relay[0:4], relay[4:8], {0, 0, 0, 0}}
		for n := 0; n < 1000; n++ {
			nonce := make([]byte, 4)
			rand.Read(nonce)
			nonces = append(nonces, nonce)
		}
		for _, nonce := range nonces {
			direct := append(append([]byte{}, nonce...), embedded...)
			if _, _, err := PacketDecrypt(direct, receiverPublic); err == nil {
				t.Fatalf("embedded packet accepted as direct packet with nonce %x", nonce)
			}
		}
	}

	// direct packet placed into a Traverse message, without its nonce field and as a whole
	for n := 0; n < 1000; n++ {
		direct, err := PacketEncrypt(senderPrivate, receiverPublic, packet)
		if err != nil {
			t.Fatal(err)
		}

		// relay Peer IDs: a regular one, and the worst case whose first 8 bytes are the direct packet's nonce twice (same key stream)
		relaySame := make(ed25519.PublicKey, ed25519.PublicKeySize)
		copy(relaySame, relayPublic)
		copy(relaySame[0:4], direct[0:4])
		copy(relaySame[4:8], direct[0:4])

		for _, relay := range []ed25519.PublicKey{relayPublic, relaySame} {
			if _, _, err := PacketDecryptEmbedded(direct[4:], receiverPublic, relay); err == nil {
				t.Fatal("direct packet without nonce accepted as embedded packet")
			}
			if _, _, err := PacketDecryptEmbedded(direct, receiverPublic, relay); err == nil {
				t.Fatal("direct packet accepted as embedded packet")
			}
		}
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
	short := 60 + PacketLengthMinEmbedded - 1
	if _, err := DecodeTraverse(&MessageRaw{PacketRaw: PacketRaw{Command: CommandTraverse, Payload: payload[:short]}}); err == nil {
		t.Fatalf("payload of %d bytes accepted", short)
	}
	if err := EncodeTraverseSetAddress(payload[:59], ipv4, 1001, 1002, ipv6, 1003, 1004); err == nil {
		t.Fatal("set address on payload of 59 bytes accepted")
	}
}
