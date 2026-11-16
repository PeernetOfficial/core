/*
File Name:  Message Encoding Local Discovery.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner

Payload of the Local Discovery message:
Offset  Size   Info
0       ?      Announcement without request records
?       64     Signature Ed25519 by the sender over the fixed public key followed by all preceding bytes of the message

The fixed public key is the one the packet is encrypted for, see the fixed key pairs of the IPv4 broadcast and the IPv6 multicast.
*/

package protocol

import (
	"crypto/ed25519"
	"errors"
)

// LocalDiscoverySign returns the payload of a Local Discovery message: the announcement followed by the signature of the sender.
// receiverPublicKey is the fixed public key that the packet is encrypted for.
func LocalDiscoverySign(announcement []byte, senderPrivateKey ed25519.PrivateKey, receiverPublicKey ed25519.PublicKey) (payload []byte) {
	signed := make([]byte, 0, len(receiverPublicKey)+len(announcement))
	signed = append(signed, receiverPublicKey...)
	signed = append(signed, announcement...)

	payload = make([]byte, 0, len(announcement)+ed25519.SignatureSize)
	payload = append(payload, announcement...)
	payload = append(payload, ed25519.Sign(senderPrivateKey, signed)...)

	return payload
}

// LocalDiscoveryVerify verifies the signature of a Local Discovery message against the sender's public key of the packet and returns the announcement.
// receiverPublicKey is the fixed public key that the packet was decrypted for.
func LocalDiscoveryVerify(payload []byte, senderPublicKey, receiverPublicKey ed25519.PublicKey) (announcement []byte, err error) {
	if len(payload) < ed25519.SignatureSize {
		return nil, errors.New("local discovery: invalid minimum length")
	} else if len(senderPublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("local discovery: invalid sender public key")
	}

	announcement = payload[:len(payload)-ed25519.SignatureSize]

	signed := make([]byte, 0, len(receiverPublicKey)+len(announcement))
	signed = append(signed, receiverPublicKey...)
	signed = append(signed, announcement...)

	if !ed25519.Verify(senderPublicKey, signed, payload[len(announcement):]) {
		return nil, errors.New("local discovery: invalid signature")
	}

	return announcement, nil
}
