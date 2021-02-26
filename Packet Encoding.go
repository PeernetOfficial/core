/*
File Name:  Packet Encoding.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner

Basic packet structure of ALL packets:
Offset  Size   Info
0       4      Nonce
4       1      Protocol version = 0
5       1      Command
6       2      Size of payload data
8       ?      Payload
        ?      Randomized garbage
?		65     Signature, ECDSA secp256k1 512-bit + 1 header byte

The peer ID of the sender, which is a ECDSA (secp256k1) 257-bit public key, can be extracted from the ECDSA signature.
The signature is applied on the entire packet, which guarantees that the signature becomes invalid should someone try to forge the receiver (i.e. forward the packet).
Because the signature could be a possible fingerpint, it is encrypted itself.
*/

package core

import (
	"encoding/binary"
	"errors"
	"math/rand"

	"github.com/btcsuite/btcd/btcec"
	"golang.org/x/crypto/salsa20"
	"lukechampine.com/blake3"
)

// PacketRaw is a decrypted P2P message
type PacketRaw struct {
	Protocol uint8  // Protocol version = 0
	Command  uint8  // 0 = Announcement
	Payload  []byte // Payload
}

// The minimum packet size is 8 bytes (minimum header size) + 65 bytes (signature)
const packetLengthMin = 8 + signatureSize
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

	senderPublicKey, _, err = btcec.RecoverCompact(btcec.S256(), signature[:], hashData(raw[:len(raw)-signatureSize]))
	if err != nil {
		return nil, nil, err
	}

	// Decrypt the packet using Salsa20.
	bufferDecrypted := make([]byte, len(raw)-signatureSize-4) // full length -signature -nonce
	salsa20.XORKeyStream(bufferDecrypted[:], raw[4:len(raw)-signatureSize], nonce, keySalsa)

	// copy all fields
	packet = &PacketRaw{Protocol: bufferDecrypted[0], Command: bufferDecrypted[1]}

	sizePayload := binary.LittleEndian.Uint16(bufferDecrypted[2:4])
	if int(sizePayload) > len(bufferDecrypted)-4 { // invalid length?
		return nil, nil, errors.New("invalid length field")
	}
	if sizePayload > 0 {
		packet.Payload = make([]byte, int(sizePayload))
		copy(packet.Payload, bufferDecrypted[4:4+int(sizePayload)])
	}

	return packet, senderPublicKey, nil
}

// PacketEncrypt encrypts a packet using the provided senders private key and receivers compressed public key.
func PacketEncrypt(senderPrivateKey *btcec.PrivateKey, receiverPublicKey *btcec.PublicKey, packet *PacketRaw) (raw []byte, err error) {
	garbage := packetGarbage(packetLengthMin + len(packet.Payload))
	raw = make([]byte, packetLengthMin+len(packet.Payload)+len(garbage))

	nonceC := rand.Uint32()
	nonce := make([]byte, 8)
	binary.LittleEndian.PutUint32(nonce[0:4], nonceC)
	binary.LittleEndian.PutUint32(nonce[4:8], nonceC)
	copy(raw[0:4], nonce[0:4])

	raw[4] = packet.Protocol
	raw[5] = packet.Command

	binary.LittleEndian.PutUint16(raw[6:8], uint16(len(packet.Payload)))
	copy(raw[8:], packet.Payload)
	copy(raw[8+len(packet.Payload):8+len(packet.Payload)+len(garbage)], garbage)

	// encrypt it using Salsa20
	keySalsa := publicKeyToSalsa20Key(receiverPublicKey)
	salsa20.XORKeyStream(raw[4:8+len(packet.Payload)+len(garbage)], raw[4:8+len(packet.Payload)+len(garbage)], nonce, keySalsa)

	// add signature
	signature, err := btcec.SignCompact(btcec.S256(), senderPrivateKey, hashData(raw[:len(raw)-signatureSize]), true)
	if err != nil {
		return nil, err
	}

	salsa20.XORKeyStream(signature[:], signature[:], nonce, keySalsa)
	copy(raw[len(raw)-signatureSize:], signature)

	return raw, nil
}

func packetGarbage(packetLength int) (random []byte) {
	// Align maximum length at 508 bytes (UDP minimum no fragmentation) and 1472 bytes (MTU).
	maxLength := maxRandomGarbage
	switch {
	case packetLength == 508, packetLength == 1472:
		return nil
	case packetLength < 508 && (508-packetLength) < maxRandomGarbage:
		maxLength = packetLength - 508
	case packetLength < 1472 && (1472-packetLength) < maxRandomGarbage:
		maxLength = packetLength - 1472
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

// hashData abstracts the hash function.
func hashData(data []byte) (hash []byte) {
	hash32 := blake3.Sum256(data)
	return hash32[:]
}
