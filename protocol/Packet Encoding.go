/*
File Name:  Packet Encoding.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Basic packet structure of ALL packets:
Offset  Size   Info
0       4      Nonce
4       1      Protocol version = 0
5       1      Command
6       4      Sequence
10      2      Size of payload data
12      ?      Payload
        ?      Randomized garbage
?		65     Signature, ECDSA secp256k1 512-bit + 1 header byte

The peer ID of the sender, which is a ECDSA (secp256k1) 257-bit public key, can be extracted from the ECDSA signature.
The signature is applied on the entire packet, which guarantees that the signature becomes invalid should someone try to forge the receiver (i.e. forward the packet).
Because the signature could be a possible fingerpint, it is encrypted itself.
*/

package protocol

import (
	"encoding/binary"
	"errors"
	"math/rand"

	"github.com/PeernetOfficial/core/btcec"
	"golang.org/x/crypto/salsa20"
)

// PacketRaw is a decrypted P2P message
type PacketRaw struct {
	Protocol uint8  // Protocol version = 0
	Command  uint8  // 0 = Announcement
	Sequence uint32 // Sequence number
	Payload  []byte // Payload
}

// The minimum packet size is 12 bytes (minimum header size) + 65 bytes (signature)
const PacketLengthMin = 12 + signatureSize
const signatureSize = 65
const maxRandomGarbage = 20

// PacketDecrypt decrypts the packet, verifies its signature and returns a high-level version of the packet.
func PacketDecrypt(raw []byte, receiverPublicKey *btcec.PublicKey) (packet *PacketRaw, senderPublicKey *btcec.PublicKey, err error) {
	// Packet is assumed to be already checked for minimum length.

	// Prepare Salsa20 nonce and key. Nonce = 2x first 4 bytes. For size reasons, only 4 bytes (instead of 8 bytes) is supplied in the packet.
	// This could be a risk, but considering we only use the PUBLIC key as decryption key, it is negligible.
	nonce := make([]byte, 8)
	copy(nonce[0:4], raw[0:4])
	copy(nonce[4:8], raw[0:4])

	// Verify the signature and extract the public key from it.
	var signature [signatureSize]byte
	copy(signature[:], raw[len(raw)-signatureSize:])
	keySalsa := publicKeyToSalsa20Key(receiverPublicKey)
	salsa20.XORKeyStream(signature[:], signature[:], nonce, keySalsa)

	senderPublicKey, _, err = btcec.RecoverCompact(btcec.S256(), signature[:], HashData(raw[:len(raw)-signatureSize]))
	if err != nil {
		return nil, nil, err
	}

	// Decrypt the packet using Salsa20.
	bufferDecrypted := make([]byte, len(raw)-signatureSize-4) // full length -signature -nonce
	salsa20.XORKeyStream(bufferDecrypted[:], raw[4:len(raw)-signatureSize], nonce, keySalsa)

	// copy all fields
	packet = &PacketRaw{Protocol: bufferDecrypted[0], Command: bufferDecrypted[1]}
	packet.Sequence = binary.LittleEndian.Uint32(bufferDecrypted[2:6])

	sizePayload := binary.LittleEndian.Uint16(bufferDecrypted[6:8])
	if int(sizePayload) > len(bufferDecrypted)-8 { // invalid length?
		return nil, nil, errors.New("invalid length field")
	}
	if sizePayload > 0 {
		packet.Payload = make([]byte, int(sizePayload))
		copy(packet.Payload, bufferDecrypted[8:8+int(sizePayload)])
	}

	return packet, senderPublicKey, nil
}

// PacketEncrypt encrypts a packet using the provided senders private key and receivers compressed public key.
func PacketEncrypt(senderPrivateKey *btcec.PrivateKey, receiverPublicKey *btcec.PublicKey, packet *PacketRaw) (raw []byte, err error) {
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
	binary.LittleEndian.PutUint16(raw[10:12], uint16(len(packet.Payload)))
	copy(raw[12:], packet.Payload)
	copy(raw[12+len(packet.Payload):12+len(packet.Payload)+len(garbage)], garbage)

	// encrypt it using Salsa20
	keySalsa := publicKeyToSalsa20Key(receiverPublicKey)
	salsa20.XORKeyStream(raw[4:12+len(packet.Payload)+len(garbage)], raw[4:12+len(packet.Payload)+len(garbage)], nonce, keySalsa)

	// add signature
	signature, err := btcec.SignCompact(btcec.S256(), senderPrivateKey, HashData(raw[:len(raw)-signatureSize]), true)
	if err != nil {
		return nil, err
	}

	salsa20.XORKeyStream(signature[:], signature[:], nonce, keySalsa)
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

func publicKeyToSalsa20Key(publicKey *btcec.PublicKey) (key *[32]byte) {
	// bit 0 from PublicKey.Y is ignored here, but is negligible for this purpose
	key = new([32]byte)
	copy(key[:], publicKey.SerializeCompressed()[1:])
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
