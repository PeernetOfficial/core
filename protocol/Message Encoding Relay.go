/*
File Name:  Message Encoding Relay.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner

Payload of the Relay message:
Offset  Size   Info
0       32     Peer ID of the other peer: the end receiver when sent to the relay, the original sender when sent by the relay.
32      1      Flags. Bit 0: FORWARDED, set by the relay. Bits 1-7 are 0.
33      ?      Embedded packet. Its size follows from the size of the payload.

The embedded packet has the packet structure without the sender's public key and without the randomized garbage, see PacketEncryptRelay.
*/

package protocol

import (
	"crypto/ed25519"
	"errors"
)

// MessageRelay is the decoded relay message.
// It is sent by an original sender to a relay, which forwards it to the end receiver in a relay message of its own.
type MessageRelay struct {
	*MessageRaw                         // Underlying raw message.
	Peer              ed25519.PublicKey // Peer ID of the other peer: the end receiver when sent to the relay, the original sender when sent by the relay.
	Forwarded         bool              // FORWARDED: set by the relay. The receiver of a message with the flag is the end receiver.
	EmbeddedPacketRaw []byte            // Embedded packet. It is decrypted and verified with PacketDecryptRelay.
}

// relayPayloadHeaderSize is the size of the payload without the embedded packet: Peer ID of the other peer (32), flags (1).
const relayPayloadHeaderSize = 33

// relayFlagForwarded is bit 0 of the flags. FORWARDED: set by the relay.
const relayFlagForwarded = 1 << 0

// DecodeRelay decodes a relay message.
// The embedded packet is returned as is. The end receiver decrypts and verifies it with PacketDecryptRelay.
func DecodeRelay(msg *MessageRaw) (result *MessageRelay, err error) {
	if len(msg.Payload) < relayPayloadHeaderSize+PacketLengthMinRelay {
		return nil, errors.New("relay: invalid minimum length")
	}

	result = &MessageRelay{
		MessageRaw:        msg,
		Peer:              make(ed25519.PublicKey, ed25519.PublicKeySize),
		EmbeddedPacketRaw: msg.Payload[relayPayloadHeaderSize:],
	}
	copy(result.Peer, msg.Payload[0:32])
	result.Forwarded = msg.Payload[32]&relayFlagForwarded > 0

	return result, nil
}

// EncodeRelay encodes a relay message. The embedded packet is created with PacketEncryptRelay; a relay forwards the one it received.
// peer is the end receiver when sent to the relay, and the original sender when sent by the relay. forwarded is set by the relay.
func EncodeRelay(embeddedPacketRaw []byte, peer ed25519.PublicKey, forwarded bool) (packetRaw []byte, err error) {
	if len(peer) != ed25519.PublicKeySize {
		return nil, errors.New("relay encode: invalid peer ID")
	} else if len(embeddedPacketRaw) < PacketLengthMinRelay {
		return nil, errors.New("relay encode: invalid embedded packet")
	} else if isPacketSizeExceed(relayPayloadHeaderSize, len(embeddedPacketRaw)) {
		return nil, errors.New("relay encode: embedded packet too big")
	}

	raw := make([]byte, relayPayloadHeaderSize+len(embeddedPacketRaw))

	copy(raw[0:32], peer)
	if forwarded {
		raw[32] = relayFlagForwarded
	}
	copy(raw[relayPayloadHeaderSize:], embeddedPacketRaw)

	return raw, nil
}
