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

	// Decrypt the packet (everything after the nonce, including the signature) using Salsa20.
	bufferDecrypted := make([]byte, len(raw)-4) // full length -nonce
	salsa20.XORKeyStream(bufferDecrypted, raw[4:], nonce, publicKeyToSalsa20Key(receiverPublicKey))

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
	garbage := packetGarbage(PacketLengthMin + len(packet.Payload))
	raw = make([]byte, PacketLengthMin+len(packet.Payload)+len(garbage))

	nonceC := rand.Uint32()
	nonce := make([]byte, 8)
	binary.LittleEndian.PutUint32(nonce[0:4], nonceC)
	binary.LittleEndian.PutUint32(nonce[4:8], nonceC)
	copy(raw[0:4], nonce[0:4])

	raw[4] = packet.Protocol
	raw[5] = packet.Command

	binary.LittleEndian.PutUint32(raw[6:10], uint32(packet.Sequence))
	copy(raw[10:42], senderPrivateKey.Public().(ed25519.PublicKey))
	binary.LittleEndian.PutUint16(raw[42:44], uint16(len(packet.Payload)))
	copy(raw[44:], packet.Payload)
	copy(raw[44+len(packet.Payload):44+len(packet.Payload)+len(garbage)], garbage)

	// encrypt it using Salsa20. One continuous key stream covers everything after the nonce, including the signature added below.
	keyStream := make([]byte, len(raw)-4)
	salsa20.XORKeyStream(keyStream, keyStream, nonce, publicKeyToSalsa20Key(receiverPublicKey))
	xorBytes(raw[4:len(raw)-signatureSize], keyStream[:len(keyStream)-signatureSize])

	// add signature over all preceding bytes as transmitted, then encrypt it
	signature := ed25519.Sign(senderPrivateKey, raw[:len(raw)-signatureSize])
	xorBytes(signature, keyStream[len(keyStream)-signatureSize:])
	copy(raw[len(raw)-signatureSize:], signature)

	return raw, nil
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
// This is important for the remote peer to make smart decisions whether this peer is behind a NAT/firewall and supports port forwarding/UPnP.
func (packet *PacketRaw) SetSelfReportedPorts(portI, portE uint16) {
	if packet.Command != CommandAnnouncement && packet.Command != CommandResponse { // only for Announcement and Response messages
		return
	}

	binary.LittleEndian.PutUint16(packet.Payload[19:19+2], portI)
	binary.LittleEndian.PutUint16(packet.Payload[21:21+2], portE)
}
