/*
File Name:  Packet Encoding.go
Copyright:  2021 Kleissner Ventures Ltd
Author:     Peter Kleissner

Basic packet structure of ALL packets:
Offset  Size   Info
0       1      Protocol version = 0
1       1      Command
2       4      Sequence
6       32     Sender's public key (= sender's Peer ID)
38      2      Size of payload data
40      ?      Payload
        ?      Randomized garbage
        8      Signature Ed25519 by the sender, first 8 bytes. They are the Salsa20 nonce.
        56     Signature, remaining 56 bytes

The signature is applied first and encryption second. The signature is computed over the receiver's Peer ID followed by all fields before the signature, in plaintext. The receiver's Peer ID is not transmitted.
The signature is therefore invalid for any other receiver (i.e. if someone forwards the packet).
The packet is encrypted with Salsa20 using the receiver's public key as key and the first 8 bytes of the signature as nonce. One key stream starts at offset 0.
The nonce is transmitted unencrypted and the 8 key stream bytes at its position are skipped. Because the signature could be a possible fingerprint, its remaining 56 bytes are encrypted.

A packet embedded in a Traverse message has the same structure without the randomized garbage.
Its signature is computed over the Peer ID of the end receiver, followed by the Peer ID of the relaying peer, followed by all fields before the signature.
It is therefore valid only inside a Traverse message sent by that relay, and not as a packet sent directly.
*/

package protocol

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"math/rand"

	"golang.org/x/crypto/salsa20"
)

// PacketRaw is a decrypted P2P message
type PacketRaw struct {
	Protocol uint8  // Protocol version = 0
	Command  uint8  // 0 = Announcement
	Sequence uint32 // Sequence number
	Payload  []byte // Payload
}

// The minimum packet size is 40 bytes (header) + 64 bytes (signature). A packet embedded in a Traverse message has exactly the minimum size plus the size of the payload.
const PacketLengthMin = packetHeaderSize + signatureSize
const packetHeaderSize = 40
const signatureSize = ed25519.SignatureSize
const maxRandomGarbage = 20

// packetNonceSize is the size of the Salsa20 nonce, which is the first bytes of the signature.
const packetNonceSize = 8

// PacketDecrypt decrypts the packet, verifies its signature and returns a high-level version of the packet.
func PacketDecrypt(raw []byte, receiverPublicKey ed25519.PublicKey) (packet *PacketRaw, senderPublicKey ed25519.PublicKey, err error) {
	// Packet is assumed to be already checked for minimum length.

	return packetDecrypt(raw, receiverPublicKey, receiverPublicKey)
}

// PacketDecryptEmbedded decrypts a packet embedded in a Traverse message, verifies its signature and returns a high-level version of the packet.
// The signature covers the Peer ID of the relaying peer. The relay passes its own Peer ID, the end receiver the Peer ID of the peer that sent the Traverse message.
func PacketDecryptEmbedded(raw []byte, receiverPublicKey, relayPublicKey ed25519.PublicKey) (packet *PacketRaw, senderPublicKey ed25519.PublicKey, err error) {
	if len(raw) < PacketLengthMin {
		return nil, nil, errors.New("embedded packet: invalid minimum length")
	} else if len(relayPublicKey) != ed25519.PublicKeySize {
		return nil, nil, errors.New("embedded packet: invalid relay peer ID")
	}

	if packet, senderPublicKey, err = packetDecrypt(raw, receiverPublicKey, signedPrefixEmbedded(receiverPublicKey, relayPublicKey)); err != nil {
		return nil, nil, err
	}

	// The embedded packet has no randomized garbage.
	if len(raw) != PacketLengthMin+len(packet.Payload) {
		return nil, nil, errors.New("embedded packet: invalid length")
	}

	return packet, senderPublicKey, nil
}

// packetDecrypt decrypts and verifies a packet. signedPrefix is the data the signature covers in front of the packet's fields:
// the receiver's Peer ID for a direct packet, the Peer IDs of the end receiver and the relay for an embedded packet.
func packetDecrypt(raw []byte, receiverPublicKey ed25519.PublicKey, signedPrefix []byte) (packet *PacketRaw, senderPublicKey ed25519.PublicKey, err error) {
	// The buffer holds the signed prefix followed by the decrypted packet.
	buffer := make([]byte, len(signedPrefix)+len(raw))
	copy(buffer, signedPrefix)
	bufferDecrypted := buffer[len(signedPrefix):]
	offsetSignature := len(raw) - signatureSize

	// Decrypt the packet using Salsa20 with one key stream starting at offset 0. The nonce is the first 8 bytes of the signature. They are transmitted unencrypted.
	nonce := raw[offsetSignature : offsetSignature+packetNonceSize]
	salsa20.XORKeyStream(bufferDecrypted, raw, nonce, publicKeyToSalsa20Key(receiverPublicKey))
	copy(bufferDecrypted[offsetSignature:], nonce)

	// Verify the signature using the sender's public key transmitted in the packet. The signature covers the signed prefix and all preceding bytes in plaintext.
	senderPublicKey = make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(senderPublicKey, bufferDecrypted[6:6+ed25519.PublicKeySize])
	signature := bufferDecrypted[offsetSignature:]

	if !ed25519.Verify(senderPublicKey, buffer[:len(buffer)-signatureSize], signature) {
		return nil, nil, errors.New("invalid signature")
	}

	// copy all fields
	packet = &PacketRaw{Protocol: bufferDecrypted[0], Command: bufferDecrypted[1]}
	packet.Sequence = binary.LittleEndian.Uint32(bufferDecrypted[2:6])

	sizePayload := binary.LittleEndian.Uint16(bufferDecrypted[38:40])
	if int(sizePayload) > len(bufferDecrypted)-40-signatureSize { // invalid length?
		return nil, nil, errors.New("invalid length field")
	}
	if sizePayload > 0 {
		packet.Payload = make([]byte, int(sizePayload))
		copy(packet.Payload, bufferDecrypted[40:40+int(sizePayload)])
	}

	return packet, senderPublicKey, nil
}

// PacketEncrypt encrypts a packet using the provided senders private key and receivers public key.
func PacketEncrypt(senderPrivateKey ed25519.PrivateKey, receiverPublicKey ed25519.PublicKey, packet *PacketRaw) (raw []byte, err error) {
	return packetEncrypt(senderPrivateKey, receiverPublicKey, receiverPublicKey, packet, packetGarbage(PacketLengthMin+len(packet.Payload))), nil
}

// PacketEncryptEmbedded encrypts a packet to be embedded in a Traverse message. It has no randomized garbage.
// The signature covers the Peer ID of the relaying peer, which is the peer the Traverse message is sent to.
func PacketEncryptEmbedded(senderPrivateKey ed25519.PrivateKey, receiverPublicKey, relayPublicKey ed25519.PublicKey, packet *PacketRaw) (raw []byte, err error) {
	if len(relayPublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("embedded packet: invalid relay peer ID")
	}

	return packetEncrypt(senderPrivateKey, receiverPublicKey, signedPrefixEmbedded(receiverPublicKey, relayPublicKey), packet, nil), nil
}

// signedPrefixEmbedded returns the data the signature of an embedded packet covers in front of the packet's fields: the Peer ID of the end receiver followed by the Peer ID of the relay.
func signedPrefixEmbedded(receiverPublicKey, relayPublicKey ed25519.PublicKey) (signedPrefix []byte) {
	signedPrefix = make([]byte, 0, len(receiverPublicKey)+len(relayPublicKey))
	signedPrefix = append(signedPrefix, receiverPublicKey...)
	return append(signedPrefix, relayPublicKey...)
}

// packetEncrypt signs and encrypts a packet. signedPrefix is the data the signature covers in front of the packet's fields:
// the receiver's Peer ID for a direct packet, the Peer IDs of the end receiver and the relay for an embedded packet. It is not part of the packet.
// garbage is appended to the payload; it is empty for an embedded packet.
func packetEncrypt(senderPrivateKey ed25519.PrivateKey, receiverPublicKey ed25519.PublicKey, signedPrefix []byte, packet *PacketRaw, garbage []byte) (raw []byte) {
	// The buffer holds the signed prefix followed by the packet.
	buffer := make([]byte, len(signedPrefix)+PacketLengthMin+len(packet.Payload)+len(garbage))
	copy(buffer, signedPrefix)
	raw = buffer[len(signedPrefix):]
	offsetSignature := len(raw) - signatureSize

	raw[0] = packet.Protocol
	raw[1] = packet.Command

	binary.LittleEndian.PutUint32(raw[2:6], uint32(packet.Sequence))
	copy(raw[6:38], senderPrivateKey.Public().(ed25519.PublicKey))
	binary.LittleEndian.PutUint16(raw[38:40], uint16(len(packet.Payload)))
	copy(raw[40:], packet.Payload)
	copy(raw[40+len(packet.Payload):offsetSignature], garbage)

	// add signature over the signed prefix and all preceding bytes in plaintext
	signature := ed25519.Sign(senderPrivateKey, buffer[:len(buffer)-signatureSize])

	// encrypt it using Salsa20 with one key stream starting at offset 0. The nonce is the first 8 bytes of the signature. They are transmitted unencrypted.
	copy(raw[offsetSignature:], signature)
	salsa20.XORKeyStream(raw, raw, signature[:packetNonceSize], publicKeyToSalsa20Key(receiverPublicKey))
	copy(raw[offsetSignature:], signature[:packetNonceSize])

	return raw
}

func packetGarbage(packetLength int) (random []byte) {
	// Align maximum length at 508 bytes (UDP minimum no fragmentation) and at a relatively safe MTU.
	maxLength := maxRandomGarbage
	switch {
	case packetLength == 508, packetLength == internetSafeMTU:
		return nil
	case packetLength < 508 && (508-packetLength) < maxRandomGarbage:
		maxLength = 508 - packetLength
	case packetLength < internetSafeMTU && (internetSafeMTU-packetLength) < maxRandomGarbage:
		maxLength = internetSafeMTU - packetLength
	}

	b := make([]byte, rand.Intn(maxLength))
	if _, err := rand.Read(b); err != nil {
		return nil
	}
	return b
}

func publicKeyToSalsa20Key(publicKey ed25519.PublicKey) (key *[32]byte) {
	key = new([32]byte)
	copy(key[:], publicKey)
	return key
}

// SetSelfReportedPorts sets the fields Internal Port and External Port according to the connection details.
// The remote peer uses them to detect whether this peer is behind a NAT and whether it uses port forwarding/UPnP.
func (packet *PacketRaw) SetSelfReportedPorts(portI, portE uint16) {
	if packet.Command != CommandAnnouncement && packet.Command != CommandResponse { // only for Announcement and Response messages
		return
	}

	// Offsets 18 and 20 of the Announcement/Response payload header, see Message Encoding Announcement.go.
	binary.LittleEndian.PutUint16(packet.Payload[18:18+2], portI)
	binary.LittleEndian.PutUint16(packet.Payload[20:20+2], portE)
}
