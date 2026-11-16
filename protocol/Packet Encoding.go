/*
File Name:  Packet Encoding.go
Copyright:  2021 Kleissner Ventures Ltd
Author:     Peter Kleissner

Basic packet structure of ALL packets:
Offset  Size   Info
0       12     Nonce
12      32     Sender's public key (= sender's Peer ID)
44      1      Protocol version = 0
45      1      Command
46      4      Sequence
50      2      Size of payload data
52      ?      Payload
        ?      Randomized garbage
?       16     Authentication tag (Poly1305)

The nonce is 12 random bytes, generated for every packet.
The sender's public key is encrypted with ChaCha20 using the receiver's public key as key and the nonce.
The fields from the protocol version to the randomized garbage are encrypted and authenticated with ChaCha20-Poly1305 using the packet key and the same nonce.

The packet key is derived from a key agreement between the sender and the receiver. Each derives it from its own private key and the public key of the other:
The Ed25519 keys are converted to X25519 keys. The shared secret is the X25519 function of the own private key and the public key of the other peer.
The packet key is the blake3 hash of the shared secret followed by the sender's public key and the receiver's public key. The two directions between two peers use different packet keys.

A packet embedded in a Traverse message has the same structure without the randomized garbage. Its additional authenticated data is the Peer ID of the relaying peer followed by the command number of the Traverse message (1 byte).
It is therefore valid only inside a Traverse message sent by that relay, and not as a packet sent directly.

A packet embedded in a Relay message has the same structure without the sender's public key and without the randomized garbage.
Its sender is the sender of the Relay message when sent to the relay, and the Peer ID in the Relay message when sent by the relay.
Its additional authenticated data is the Peer ID of the relaying peer followed by the command number of the Relay message (1 byte).
*/

package protocol

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"math/rand"
	"sync"

	"filippo.io/edwards25519"
	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/chacha20poly1305"
	"lukechampine.com/blake3"
)

// PacketRaw is a decrypted P2P message
type PacketRaw struct {
	Protocol uint8  // Protocol version = 0
	Command  uint8  // 0 = Announcement
	Sequence uint32 // Sequence number
	Payload  []byte // Payload
}

// The minimum packet size is 52 bytes (header) + 16 bytes (authentication tag)
const PacketLengthMin = packetHeaderSize + packetTagSize
const packetHeaderSize = packetOffsetEncrypted + 8
const packetNonceSize = chacha20poly1305.NonceSize
const packetTagSize = chacha20poly1305.Overhead

// packetOffsetEncrypted is the offset of the protocol version, the first field that is encrypted with the packet key. The nonce and the sender's public key precede it.
const packetOffsetEncrypted = packetNonceSize + ed25519.PublicKeySize

// PacketLengthMinEmbedded is the minimum size of a packet embedded in a Traverse message. Its size is the minimum size plus the size of the payload.
const PacketLengthMinEmbedded = PacketLengthMin

// PacketLengthMinRelay is the minimum size of a packet embedded in a Relay message, which has no sender's public key. Its size is the minimum size plus the size of the payload.
const PacketLengthMinRelay = PacketLengthMin - ed25519.PublicKeySize
const maxRandomGarbage = 20

// KeyPair is the key pair of a peer for encrypting and decrypting packets. It holds the X25519 private key that is derived from the Ed25519 private key.
// It is created once per key pair and used for all packets.
type KeyPair struct {
	PublicKey ed25519.PublicKey // Public key (= Peer ID)
	x25519    *ecdh.PrivateKey  // X25519 private key for the key agreement
}

// NewKeyPair returns the key pair for encrypting and decrypting packets.
// The X25519 private key is the first 32 bytes of the SHA-512 hash of the 32-byte Ed25519 private key.
func NewKeyPair(privateKey ed25519.PrivateKey) (keyPair *KeyPair, err error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid private key")
	}

	hash := sha512.Sum512(privateKey.Seed())

	keyPair = &KeyPair{PublicKey: privateKey.Public().(ed25519.PublicKey)}
	if keyPair.x25519, err = ecdh.X25519().NewPrivateKey(hash[:32]); err != nil {
		return nil, err
	}

	return keyPair, nil
}

// sharedSecret returns the shared secret between the key pair and the other peer.
// It fails if the other public key is not a valid Ed25519 public key or if the shared secret is all zero.
func (keyPair *KeyPair) sharedSecret(otherPublicKey ed25519.PublicKey) (sharedSecret []byte, err error) {
	if len(otherPublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("invalid public key")
	}

	// X25519 public key u = (1 + y) / (1 - y), where y is the y-coordinate encoded in the Ed25519 public key
	point, err := new(edwards25519.Point).SetBytes(otherPublicKey)
	if err != nil {
		return nil, errors.New("invalid public key")
	}
	x25519PublicKey, err := ecdh.X25519().NewPublicKey(point.BytesMontgomery())
	if err != nil {
		return nil, err
	}

	// ECDH returns an error if the shared secret is all zero.
	if sharedSecret, err = keyPair.x25519.ECDH(x25519PublicKey); err != nil {
		return nil, errors.New("invalid shared secret")
	}

	return sharedSecret, nil
}

// packetKey returns the key for packets from the sender to the receiver: the blake3 hash of the shared secret followed by the sender's public key and the receiver's public key.
func packetKey(sharedSecret []byte, senderPublicKey, receiverPublicKey ed25519.PublicKey) (key []byte) {
	data := make([]byte, 0, len(sharedSecret)+2*ed25519.PublicKeySize)
	data = append(data, sharedSecret...)
	data = append(data, senderPublicKey...)
	data = append(data, receiverPublicKey...)
	hash := blake3.Sum256(data)

	return hash[:]
}

// PacketKeys caches the two packet keys between a key pair and one other peer. Both are derived and stored when the first packet needs one of them.
// The zero value is an empty cache. It is safe for concurrent use.
type PacketKeys struct {
	sync.RWMutex
	publicKey ed25519.PublicKey // Public key of the other peer that the keys were derived for.
	send      []byte            // Key for packets from the key pair to the other peer.
	receive   []byte            // Key for packets from the other peer to the key pair.
}

// packetKeyCached returns the key for packets from the key pair to the other peer (send), or from the other peer to the key pair.
// It is taken from the cache if it is there, otherwise the keys are derived and stored. keys may be nil, in which case they are derived.
// The cached keys are only used if they were derived for the other public key. The sender's public key of a received packet is not authenticated before the packet key is known:
// with the cached key of another peer, a packet of that peer would be accepted under the sender's public key it carries.
func (keyPair *KeyPair) packetKeyCached(keys *PacketKeys, otherPublicKey ed25519.PublicKey, send bool) (key []byte, err error) {
	var keySend, keyReceive []byte
	found, empty := false, false

	if keys != nil {
		keys.RLock()
		found, empty = bytes.Equal(keys.publicKey, otherPublicKey), keys.publicKey == nil
		keySend, keyReceive = keys.send, keys.receive
		keys.RUnlock()
	}

	if !found {
		sharedSecret, err := keyPair.sharedSecret(otherPublicKey)
		if err != nil {
			return nil, err
		}
		keySend = packetKey(sharedSecret, keyPair.PublicKey, otherPublicKey)
		keyReceive = packetKey(sharedSecret, otherPublicKey, keyPair.PublicKey)

		if empty {
			keys.Lock()
			if keys.publicKey == nil {
				keys.publicKey, keys.send, keys.receive = bytes.Clone(otherPublicKey), keySend, keyReceive
			}
			keys.Unlock()
		}
	}

	if send {
		return keySend, nil
	}
	return keyReceive, nil
}

// packetSenderXOR encrypts or decrypts the sender's public key of a packet with ChaCha20 using the receiver's public key as key and the nonce of the packet.
func packetSenderXOR(dst, src, nonce []byte, receiverPublicKey ed25519.PublicKey) (err error) {
	cipher, err := chacha20.NewUnauthenticatedCipher(receiverPublicKey, nonce)
	if err != nil {
		return err
	}

	cipher.XORKeyStream(dst, src)
	return nil
}

// PacketDecrypt decrypts the packet, verifies its authentication tag and returns a high-level version of the packet.
// receiver is the key pair that the packet is encrypted for.
// keys is the cache of the packet keys between the receiver and the sender of the packet, see PacketSender. It may be nil.
func PacketDecrypt(raw []byte, receiver *KeyPair, keys *PacketKeys) (packet *PacketRaw, senderPublicKey ed25519.PublicKey, err error) {
	packet, senderPublicKey, _, err = packetDecrypt(raw, receiver, nil, keys)
	return packet, senderPublicKey, err
}

// embeddedAdditionalData returns the additional authenticated data of an embedded packet:
// the Peer ID of the relaying peer followed by the command number of the message the packet is embedded in (1 byte).
func embeddedAdditionalData(relayPublicKey ed25519.PublicKey, command uint8) (additionalData []byte) {
	additionalData = make([]byte, 0, ed25519.PublicKeySize+1)
	additionalData = append(additionalData, relayPublicKey...)
	return append(additionalData, command)
}

// PacketDecryptEmbedded decrypts a packet embedded in a Traverse message, verifies its authentication tag and returns a high-level version of the packet.
// The additional authenticated data is the Peer ID of the relaying peer, which is the peer that sent the Traverse message, followed by the command number of the Traverse message.
func PacketDecryptEmbedded(raw []byte, receiver *KeyPair, relayPublicKey ed25519.PublicKey) (packet *PacketRaw, senderPublicKey ed25519.PublicKey, err error) {
	if len(relayPublicKey) != ed25519.PublicKeySize {
		return nil, nil, errors.New("embedded packet: invalid relay peer ID")
	}

	packet, senderPublicKey, sizeGarbage, err := packetDecrypt(raw, receiver, embeddedAdditionalData(relayPublicKey, CommandTraverse), nil)
	if err != nil {
		return nil, nil, err
	}

	// The embedded packet has no randomized garbage.
	if sizeGarbage != 0 {
		return nil, nil, errors.New("embedded packet: invalid length")
	}

	return packet, senderPublicKey, nil
}

// PacketEmbeddedSender returns the sender's public key of a packet embedded in a Traverse message. receiverPublicKey is the Peer ID of the end receiver.
// The relaying peer uses it; it cannot read the other fields of the embedded packet. The sender is not authenticated.
func PacketEmbeddedSender(raw []byte, receiverPublicKey ed25519.PublicKey) (senderPublicKey ed25519.PublicKey, err error) {
	if len(raw) < PacketLengthMinEmbedded {
		return nil, errors.New("embedded packet: invalid minimum length")
	}

	return PacketSender(raw, receiverPublicKey)
}

// PacketDecryptRelay decrypts a packet embedded in a Relay message, verifies its authentication tag and returns a high-level version of the packet.
// senderPublicKey is the sender of the embedded packet: the Peer ID in the Relay message that the relaying peer sent.
// The additional authenticated data is the Peer ID of the relaying peer, which is the peer that sent the Relay message, followed by the command number of the Relay message.
// keys is the cache of the packet keys between the receiver and the sender of the embedded packet. It may be nil.
func PacketDecryptRelay(raw []byte, receiver *KeyPair, senderPublicKey, relayPublicKey ed25519.PublicKey, keys *PacketKeys) (packet *PacketRaw, err error) {
	if receiver == nil {
		return nil, errors.New("invalid receiver key pair")
	} else if len(relayPublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("embedded packet: invalid relay peer ID")
	} else if len(raw) < PacketLengthMinRelay {
		return nil, errors.New("embedded packet: invalid minimum length")
	}

	key, err := receiver.packetKeyCached(keys, senderPublicKey, false)
	if err != nil {
		return nil, err
	}

	packet, sizeGarbage, err := packetDecryptFields(raw[packetNonceSize:], raw[:packetNonceSize], key, embeddedAdditionalData(relayPublicKey, CommandRelay))
	if err != nil {
		return nil, err
	}

	// The embedded packet has no randomized garbage.
	if sizeGarbage != 0 {
		return nil, errors.New("embedded packet: invalid length")
	}

	return packet, nil
}

// PacketSender returns the sender's public key of a packet. receiverPublicKey is the Peer ID of the receiver. The sender is not authenticated.
// The receiver uses it to find the cached packet keys of the sender before it decrypts the packet.
func PacketSender(raw []byte, receiverPublicKey ed25519.PublicKey) (senderPublicKey ed25519.PublicKey, err error) {
	if len(raw) < PacketLengthMin {
		return nil, errors.New("invalid minimum length")
	}

	senderPublicKey = make(ed25519.PublicKey, ed25519.PublicKeySize)
	if err = packetSenderXOR(senderPublicKey, raw[packetNonceSize:packetOffsetEncrypted], raw[:packetNonceSize], receiverPublicKey); err != nil {
		return nil, err
	}

	return senderPublicKey, nil
}

// packetDecrypt decrypts and verifies a packet. sizeGarbage is the size of the randomized garbage after the payload.
func packetDecrypt(raw []byte, receiver *KeyPair, additionalData []byte, keys *PacketKeys) (packet *PacketRaw, senderPublicKey ed25519.PublicKey, sizeGarbage int, err error) {
	if receiver == nil {
		return nil, nil, 0, errors.New("invalid receiver key pair")
	} else if len(raw) < PacketLengthMin {
		return nil, nil, 0, errors.New("invalid minimum length")
	}

	nonce := raw[:packetNonceSize]

	// Decrypt the sender's public key and derive the packet key.
	senderPublicKey = make(ed25519.PublicKey, ed25519.PublicKeySize)
	if err = packetSenderXOR(senderPublicKey, raw[packetNonceSize:packetOffsetEncrypted], nonce, receiver.PublicKey); err != nil {
		return nil, nil, 0, err
	}

	key, err := receiver.packetKeyCached(keys, senderPublicKey, false)
	if err != nil {
		return nil, nil, 0, err
	}

	if packet, sizeGarbage, err = packetDecryptFields(raw[packetOffsetEncrypted:], nonce, key, additionalData); err != nil {
		return nil, nil, 0, err
	}

	return packet, senderPublicKey, sizeGarbage, nil
}

// packetDecryptFields decrypts the fields from the protocol version to the randomized garbage with the packet key and verifies the authentication tag that follows them.
// sizeGarbage is the size of the randomized garbage after the payload.
func packetDecryptFields(encrypted, nonce, key, additionalData []byte) (packet *PacketRaw, sizeGarbage int, err error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, 0, err
	}

	decrypted, err := aead.Open(nil, nonce, encrypted, additionalData)
	if err != nil {
		return nil, 0, errors.New("invalid authentication tag")
	}

	// copy all fields
	packet = &PacketRaw{Protocol: decrypted[0], Command: decrypted[1]}
	packet.Sequence = binary.LittleEndian.Uint32(decrypted[2:6])

	sizePayload := int(binary.LittleEndian.Uint16(decrypted[6:8]))
	if sizePayload > len(decrypted)-8 { // invalid length?
		return nil, 0, errors.New("invalid length field")
	}
	if sizePayload > 0 {
		packet.Payload = make([]byte, sizePayload)
		copy(packet.Payload, decrypted[8:8+sizePayload])
	}

	return packet, len(decrypted) - 8 - sizePayload, nil
}

// PacketEncrypt encrypts a packet using the provided senders key pair and receivers public key.
// keys is the cache of the packet keys between the sender and the receiver. It may be nil.
func PacketEncrypt(sender *KeyPair, receiverPublicKey ed25519.PublicKey, packet *PacketRaw, keys *PacketKeys) (raw []byte, err error) {
	nonce := make([]byte, packetNonceSize)
	if _, err = cryptorand.Read(nonce); err != nil {
		return nil, err
	}

	return packetEncrypt(sender, receiverPublicKey, nonce, nil, packet, packetGarbage(PacketLengthMin+len(packet.Payload)), keys)
}

// PacketEncryptEmbedded encrypts a packet to be embedded in a Traverse message. It has its own nonce and no randomized garbage.
// The additional authenticated data is the Peer ID of the relaying peer, which is the peer the Traverse message is sent to, followed by the command number of the Traverse message.
func PacketEncryptEmbedded(sender *KeyPair, receiverPublicKey, relayPublicKey ed25519.PublicKey, packet *PacketRaw) (raw []byte, err error) {
	if len(relayPublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("embedded packet: invalid relay peer ID")
	}

	nonce := make([]byte, packetNonceSize)
	if _, err = cryptorand.Read(nonce); err != nil {
		return nil, err
	}

	return packetEncrypt(sender, receiverPublicKey, nonce, embeddedAdditionalData(relayPublicKey, CommandTraverse), packet, nil, nil)
}

// PacketEncryptRelay encrypts a packet to be embedded in a Relay message. It has its own nonce, no sender's public key and no randomized garbage.
// The additional authenticated data is the Peer ID of the relaying peer, which is the peer the Relay message is sent to, followed by the command number of the Relay message.
func PacketEncryptRelay(sender *KeyPair, receiverPublicKey, relayPublicKey ed25519.PublicKey, packet *PacketRaw) (raw []byte, err error) {
	if len(relayPublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("embedded packet: invalid relay peer ID")
	}

	nonce := make([]byte, packetNonceSize)
	if _, err = cryptorand.Read(nonce); err != nil {
		return nil, err
	}

	return packetEncryptRelay(sender, receiverPublicKey, nonce, embeddedAdditionalData(relayPublicKey, CommandRelay), packet)
}

// packetEncryptRelay encrypts and authenticates a packet without the sender's public key and without randomized garbage.
func packetEncryptRelay(sender *KeyPair, receiverPublicKey ed25519.PublicKey, nonce, additionalData []byte, packet *PacketRaw) (raw []byte, err error) {
	if sender == nil {
		return nil, errors.New("invalid sender key pair")
	}

	key, err := sender.packetKeyCached(nil, receiverPublicKey, true)
	if err != nil {
		return nil, err
	}

	raw = make([]byte, packetNonceSize, PacketLengthMinRelay+len(packet.Payload))
	copy(raw, nonce)

	return packetEncryptFields(raw, nonce, key, additionalData, packet, nil)
}

// packetEncrypt encrypts and authenticates a packet. additionalData is empty for a direct packet.
// garbage is appended to the payload; it is empty for an embedded packet.
func packetEncrypt(sender *KeyPair, receiverPublicKey ed25519.PublicKey, nonce, additionalData []byte, packet *PacketRaw, garbage []byte, keys *PacketKeys) (raw []byte, err error) {
	if sender == nil {
		return nil, errors.New("invalid sender key pair")
	}

	key, err := sender.packetKeyCached(keys, receiverPublicKey, true)
	if err != nil {
		return nil, err
	}

	raw = make([]byte, packetOffsetEncrypted, PacketLengthMin+len(packet.Payload)+len(garbage))

	copy(raw[0:packetNonceSize], nonce)

	if err = packetSenderXOR(raw[packetNonceSize:packetOffsetEncrypted], sender.PublicKey, nonce, receiverPublicKey); err != nil {
		return nil, err
	}

	return packetEncryptFields(raw, nonce, key, additionalData, packet, garbage)
}

// packetEncryptFields encrypts and authenticates the fields from the protocol version to the randomized garbage with the packet key.
// They are appended to raw, which holds the fields that precede them, followed by the authentication tag.
func packetEncryptFields(raw, nonce, key, additionalData []byte, packet *PacketRaw, garbage []byte) (result []byte, err error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}

	fields := make([]byte, 8+len(packet.Payload)+len(garbage))
	fields[0] = packet.Protocol
	fields[1] = packet.Command
	binary.LittleEndian.PutUint32(fields[2:6], uint32(packet.Sequence))
	binary.LittleEndian.PutUint16(fields[6:8], uint16(len(packet.Payload)))
	copy(fields[8:], packet.Payload)
	copy(fields[8+len(packet.Payload):], garbage)

	return aead.Seal(raw, nonce, fields, additionalData), nil
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
