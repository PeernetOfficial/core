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

// TestTraverseRoundTrip encodes a traverse message and verifies its signature against the embedded packet's sender.
func TestTraverseRoundTrip(t *testing.T) {
	senderPublic, senderPrivate := generateKey(t)
	relayPublic, _ := generateKey(t)
	targetPublic, _ := generateKey(t)
	otherPublic, _ := generateKey(t)

	embedded, err := PacketEncrypt(senderPrivate, targetPublic, &PacketRaw{Protocol: ProtocolVersion, Command: CommandAnnouncement, Sequence: 7, Payload: []byte("hello")})
	if err != nil {
		t.Fatal(err)
	}

	payload, err := EncodeTraverse(senderPrivate, embedded, targetPublic, relayPublic)
	if err != nil {
		t.Fatal(err)
	}

	msg, err := DecodeTraverse(&MessageRaw{PacketRaw: PacketRaw{Command: CommandTraverse, Payload: payload}, SenderPublicKey: senderPublic})
	if err != nil {
		t.Fatal(err)
	}
	if !msg.TargetPeer.Equal(targetPublic) || !msg.AuthorizedRelayPeer.Equal(relayPublic) || !bytes.Equal(msg.EmbeddedPacketRaw, embedded) {
		t.Fatal("traverse fields mismatch")
	}
	if !msg.VerifySignature(senderPublic) {
		t.Fatal("traverse signature does not verify against sender")
	}
	if msg.VerifySignature(otherPublic) {
		t.Fatal("traverse signature verifies against wrong key")
	}

	// the embedded packet identifies the sender
	_, embeddedSender, err := PacketDecrypt(msg.EmbeddedPacketRaw, targetPublic)
	if err != nil {
		t.Fatal(err)
	}
	if !embeddedSender.Equal(senderPublic) {
		t.Fatal("embedded packet sender mismatch")
	}
}
