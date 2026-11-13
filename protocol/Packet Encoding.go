/*
File Name:  Packet Encoding.go
Copyright:  2021 Kleissner Ventures Ltd
Author:     Peter Kleissner

Basic packet structure of ALL packets:
Offset  Size   Info
0       4      Nonce
4       1      Protocol version = 0
5       1      Command
6       4      Sequence
10      32     Sender's public key (= sender's Peer ID)
42      2      Size of payload data
44      ?      Payload
        ?      Randomized garbage
?       64     Signature Ed25519 by the sender over all preceding bytes

Everything after the nonce is encrypted with Salsa20 using the receiver's public key as key, including the signature.
The signature is applied on the entire packet as transmitted (i.e. encrypted), which guarantees that the signature becomes invalid should someone try to forge the receiver (i.e. forward the packet).
Because the signature could be a possible fingerprint, it is encrypted itself.

A packet embedded in a Traverse message has the same structure without the nonce field and without the randomized garbage. Its Salsa20 nonce is the first 8 bytes of the Peer ID of the relaying peer.
The signature covers all preceding bytes of the embedded packet. It is therefore valid only inside a Traverse message sent by that relay, and not as a packet sent directly.
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

// The minimum packet size is 44 bytes (header) + 64 bytes (signature)
const PacketLengthMin = packetHeaderSize + signatureSize
const packetHeaderSize = 44
const packetNonceSize = 4

// PacketLengthMinEmbedded is the minimum size of a packet embedded in a Traverse message. It has no nonce field. Its size is the minimum size plus the size of the payload.
const PacketLengthMinEmbedded = PacketLengthMin - packetNonceSize
const signatureSize = ed25519.SignatureSize
const maxRandomGarbage = 20

// PacketDecrypt decrypts the packet, verifies its signature and returns a high-level version of the packet.
func PacketDecrypt(raw []byte, receiverPublicKey ed25519.PublicKey) (packet *PacketRaw, senderPublicKey ed25519.PublicKey, err error) {
	// Packet is assumed to be already checked for minimum length.

	// Prepare Salsa20 nonce and key. Nonce = 2x first 4 bytes. For size reasons, only 4 bytes (instead of 8 bytes) is supplied in the packet.
	// This could be a risk, but considering we only use the PUBLIC key as decryption key, it is negligible.
	nonce := make([]byte, 8)
	copy(nonce[0:4], raw[0:4])
	copy(nonce[4:8], raw[0:4])

	return packetDecrypt(raw, packetNonceSize, nonce, receiverPublicKey)
}

// PacketDecryptEmbedded decrypts a packet embedded in a Traverse message, verifies its signature and returns a high-level version of the packet.
// The Salsa20 nonce is the first 8 bytes of the Peer ID of the relaying peer. The relay passes its own Peer ID, the end receiver the Peer ID of the peer that sent the Traverse message.
func PacketDecryptEmbedded(raw []byte, receiverPublicKey, relayPublicKey ed25519.PublicKey) (packet *PacketRaw, senderPublicKey ed25519.PublicKey, err error) {
	if len(raw) < PacketLengthMinEmbedded {
		return nil, nil, errors.New("embedded packet: invalid minimum length")
	} else if len(relayPublicKey) != ed25519.PublicKeySize {
		return nil, nil, errors.New("embedded packet: invalid relay peer ID")
	}

	if packet, senderPublicKey, err = packetDecrypt(raw, 0, relayPublicKey[:8], receiverPublicKey); err != nil {
		return nil, nil, err
	}

	// The embedded packet has no randomized garbage.
	if len(raw) != PacketLengthMinEmbedded+len(packet.Payload) {
		return nil, nil, errors.New("embedded packet: invalid length")
	}

	return packet, senderPublicKey, nil
}

// packetDecrypt decrypts and verifies a packet. nonceFieldSize is the size of the nonce field at the start of the packet, which is 0 for an embedded packet.
func packetDecrypt(raw []byte, nonceFieldSize int, nonce []byte, receiverPublicKey ed25519.PublicKey) (packet *PacketRaw, senderPublicKey ed25519.PublicKey, err error) {
	// Decrypt the packet (everything after the nonce field, including the signature) using Salsa20.
	bufferDecrypted := make([]byte, len(raw)-nonceFieldSize)
	salsa20.XORKeyStream(bufferDecrypted, raw[nonceFieldSize:], nonce, publicKeyToSalsa20Key(receiverPublicKey))

	// Verify the signature using the sender's public key transmitted in the packet. The signature covers all preceding bytes as transmitted.
	senderPublicKey = make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(senderPublicKey, bufferDecrypted[6:6+ed25519.PublicKeySize])
	signature := bufferDecrypted[len(bufferDecrypted)-signatureSize:]

	if !ed25519.Verify(senderPublicKey, raw[:len(raw)-signatureSize], signature) {
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
	nonceC := rand.Uint32()
	nonce := make([]byte, 8)
	binary.LittleEndian.PutUint32(nonce[0:4], nonceC)
	binary.LittleEndian.PutUint32(nonce[4:8], nonceC)

	return packetEncrypt(senderPrivateKey, receiverPublicKey, nonce[0:4], nonce, packet, packetGarbage(PacketLengthMin+len(packet.Payload))), nil
}

// PacketEncryptEmbedded encrypts a packet to be embedded in a Traverse message. It has no nonce field and no randomized garbage.
// The Salsa20 nonce is the first 8 bytes of the Peer ID of the relaying peer, which is the peer the Traverse message is sent to.
func PacketEncryptEmbedded(senderPrivateKey ed25519.PrivateKey, receiverPublicKey, relayPublicKey ed25519.PublicKey, packet *PacketRaw) (raw []byte, err error) {
	if len(relayPublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("embedded packet: invalid relay peer ID")
	}

	return packetEncrypt(senderPrivateKey, receiverPublicKey, nil, relayPublicKey[:8], packet, nil), nil
}

// packetEncrypt encrypts and signs a packet. nonceField is transmitted at the start of the packet; it is empty for an embedded packet.
// garbage is appended to the payload; it is empty for an embedded packet.
func packetEncrypt(senderPrivateKey ed25519.PrivateKey, receiverPublicKey ed25519.PublicKey, nonceField, nonce []byte, packet *PacketRaw, garbage []byte) (raw []byte) {
	o := len(nonceField) // offset of the encrypted fields
	lengthMin := o + packetHeaderSize - packetNonceSize + signatureSize

	raw = make([]byte, lengthMin+len(packet.Payload)+len(garbage))

	copy(raw[0:o], nonceField)

	raw[o] = packet.Protocol
	raw[o+1] = packet.Command

	binary.LittleEndian.PutUint32(raw[o+2:o+6], uint32(packet.Sequence))
	copy(raw[o+6:o+38], senderPrivateKey.Public().(ed25519.PublicKey))
	binary.LittleEndian.PutUint16(raw[o+38:o+40], uint16(len(packet.Payload)))
	copy(raw[o+40:], packet.Payload)
	copy(raw[o+40+len(packet.Payload):o+40+len(packet.Payload)+len(garbage)], garbage)

	// encrypt it using Salsa20. One continuous key stream covers everything after the nonce field, including the signature added below.
	keyStream := make([]byte, len(raw)-o)
	salsa20.XORKeyStream(keyStream, keyStream, nonce, publicKeyToSalsa20Key(receiverPublicKey))
	xorBytes(raw[o:len(raw)-signatureSize], keyStream[:len(keyStream)-signatureSize])

	// add signature over all preceding bytes as transmitted, then encrypt it
	signature := ed25519.Sign(senderPrivateKey, raw[:len(raw)-signatureSize])
	xorBytes(signature, keyStream[len(keyStream)-signatureSize:])
	copy(raw[len(raw)-signatureSize:], signature)

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

// xorBytes XORs each byte of data with the corresponding byte of mask, in place. Both must have the same length.
func xorBytes(data, mask []byte) {
	for n := range data {
		data[n] ^= mask[n]
	}
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
