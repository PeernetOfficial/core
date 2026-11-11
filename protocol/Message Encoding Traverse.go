/*
File Name:  Message Encoding Traverse.go
Copyright:  2021 Kleissner Ventures Ltd
Author:     Peter Kleissner

Payload of the Traverse message:
Offset  Size   Info
0       32     End receiver Peer ID
32      4      IPv4 address of the original sender. Set by the relay.
36      2      IPv4 port used for connection
38      2      IPv4 external port as reported by the original sender
40      16     IPv6 address of the original sender. Set by the relay.
56      2      IPv6 port used for connection
58      2      IPv6 external port as reported by the original sender
60      ?      Embedded packet. Its size follows from the size of the payload.

The embedded packet has the packet structure without the nonce and without the randomized garbage, see PacketEncryptEmbedded.
*/

package protocol

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"net"
)

// MessageTraverse is the decoded traverse message.
// It is sent by an original sender to a relay, which forwards it to the end receiver (target peer).
type MessageTraverse struct {
	*MessageRaw                                // Underlying raw message.
	TargetPeer               ed25519.PublicKey // End receiver peer ID.
	IPv4                     net.IP            // IPv4 address of the original sender. Set by the relay. 0 if not set.
	PortIPv4                 uint16            // Port (actual one used for connection) of the original sender. Set by the relay.
	PortIPv4ReportedExternal uint16            // External port as reported by the original sender. This is used in case of port forwarding (manual or automated).
	IPv6                     net.IP            // IPv6 address of the original sender. Set by the relay. 0 if not set.
	PortIPv6                 uint16            // Port (actual one used for connection) of the original sender. Set by the relay.
	PortIPv6ReportedExternal uint16            // External port as reported by the original sender. This is used in case of port forwarding (manual or automated).
	EmbeddedPacketRaw        []byte            // Embedded packet. It is decrypted and verified with PacketDecryptEmbedded.
}

// traversePayloadHeaderSize is the size of the payload without the embedded packet: end receiver peer ID (32), addresses and ports (28).
const traversePayloadHeaderSize = 60

// DecodeTraverse decodes a traverse message.
// The embedded packet is returned as is. The caller decrypts and verifies it with PacketDecryptEmbedded.
func DecodeTraverse(msg *MessageRaw) (result *MessageTraverse, err error) {
	result = &MessageTraverse{
		MessageRaw: msg,
	}

	if len(msg.Payload) < traversePayloadHeaderSize+PacketLengthMinEmbedded {
		return nil, errors.New("traverse: invalid minimum length")
	}

	result.TargetPeer = make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(result.TargetPeer, msg.Payload[0:32])

	// IPv4
	ipv4B := make([]byte, 4)
	copy(ipv4B[:], msg.Payload[32:32+4])

	result.IPv4 = ipv4B
	result.PortIPv4 = binary.LittleEndian.Uint16(msg.Payload[36 : 36+2])
	result.PortIPv4ReportedExternal = binary.LittleEndian.Uint16(msg.Payload[38 : 38+2])

	// IPv6
	ipv6B := make([]byte, 16)
	copy(ipv6B[:], msg.Payload[40:40+16])

	result.IPv6 = ipv6B
	result.PortIPv6 = binary.LittleEndian.Uint16(msg.Payload[56 : 56+2])
	result.PortIPv6ReportedExternal = binary.LittleEndian.Uint16(msg.Payload[58 : 58+2])

	if result.IPv6.To4() != nil {
		return nil, errors.New("traverse: ipv6 address mismatch")
	}

	result.EmbeddedPacketRaw = msg.Payload[traversePayloadHeaderSize:]

	return result, nil
}

// EncodeTraverse encodes a traverse message. The embedded packet is created with PacketEncryptEmbedded.
// The addresses and ports are left zero; they are set by the relay.
func EncodeTraverse(embeddedPacketRaw []byte, receiverEnd ed25519.PublicKey) (packetRaw []byte, err error) {
	sizePacketEmbed := len(embeddedPacketRaw)
	if isPacketSizeExceed(traversePayloadHeaderSize, sizePacketEmbed) {
		return nil, errors.New("traverse encode: embedded packet too big")
	}

	raw := make([]byte, traversePayloadHeaderSize+sizePacketEmbed)

	copy(raw[0:32], receiverEnd)
	copy(raw[traversePayloadHeaderSize:], embeddedPacketRaw)

	return raw, nil
}

// EncodeTraverseSetAddress sets the IP and Port in a traverse message that shall be forwarded to another peer
func EncodeTraverseSetAddress(raw []byte, IPv4 net.IP, PortIPv4, PortIPv4ReportedExternal uint16, IPv6 net.IP, PortIPv6, PortIPv6ReportedExternal uint16) (err error) {
	if isPacketSizeExceed(len(raw), 0) {
		return errors.New("traverse encode 2: embedded packet too big")
	} else if len(raw) < traversePayloadHeaderSize {
		return errors.New("traverse encode 2: invalid packet")
	}

	// IPv4
	if IPv4 != nil && len(IPv4) == net.IPv4len {
		copy(raw[32:32+4], IPv4.To4())
		binary.LittleEndian.PutUint16(raw[36:36+2], PortIPv4)
		binary.LittleEndian.PutUint16(raw[38:38+2], PortIPv4ReportedExternal)
	}

	// IPv6
	if IPv6 != nil && len(IPv6) == net.IPv6len {
		copy(raw[40:40+16], IPv6.To16())
		binary.LittleEndian.PutUint16(raw[56:56+2], PortIPv6)
		binary.LittleEndian.PutUint16(raw[58:58+2], PortIPv6ReportedExternal)
	}

	return nil
}
