/*
File Name:  Message Encoding Traverse.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package protocol

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"net"
	"time"

)

// MessageTraverse is the decoded traverse message.
// It is sent by an original sender to a relay, to a final receiver (targert peer).
type MessageTraverse struct {
	*MessageRaw                               // Underlying raw message.
	TargetPeer               ed25519.PublicKey // End receiver peer ID.
	AuthorizedRelayPeer      ed25519.PublicKey // Peer ID that is authorized to relay this message to the end receiver.
	Expires                  time.Time         // Expiration time when this forwarded message becomes invalid.
	EmbeddedPacketRaw        []byte            // Embedded packet.
	Signature                []byte            // Signature by the original sender over all preceding bytes. Verified against the sender of the embedded packet, see VerifySignature.
	IPv4                     net.IP            // IPv4 address of the original sender. Set by authorized relay. 0 if not set.
	PortIPv4                 uint16            // Port (actual one used for connection) of the original sender. Set by authorized relay.
	PortIPv4ReportedExternal uint16            // External port as reported by the original sender. This is used in case of port forwarding (manual or automated).
	IPv6                     net.IP            // IPv6 address of the original sender. Set by authorized relay. 0 if not set.
	PortIPv6                 uint16            // Port (actual one used for connection) of the original sender. Set by authorized relay.
	PortIPv6ReportedExternal uint16            // External port as reported by the original sender. This is used in case of port forwarding (manual or automated).
}

const traversePayloadHeaderSize = 76 + signatureSize + 28

// DecodeTraverse decodes a traverse message.
// It does not verify if the receiver is authorized to read or forward this message.
// It does not verify the signature: Ed25519 verification requires the signer's public key, which is known from the embedded packet. The caller must call VerifySignature.
func DecodeTraverse(msg *MessageRaw) (result *MessageTraverse, err error) {
	result = &MessageTraverse{
		MessageRaw: msg,
	}

	if len(msg.Payload) < traversePayloadHeaderSize {
		return nil, errors.New("traverse: invalid minimum length")
	}

	result.TargetPeer = make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(result.TargetPeer, msg.Payload[0:32])
	result.AuthorizedRelayPeer = make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(result.AuthorizedRelayPeer, msg.Payload[32:64])

	// receiver and target must not be the same
	if result.TargetPeer.Equal(result.AuthorizedRelayPeer) {
		return nil, errors.New("traverse: target and relay invalid")
	}

	expires64 := binary.LittleEndian.Uint64(msg.Payload[64 : 64+8])
	result.Expires = time.Unix(int64(expires64), 0)

	sizePacketEmbed := binary.LittleEndian.Uint16(msg.Payload[74 : 74+2])
	if int(sizePacketEmbed) != len(msg.Payload)-traversePayloadHeaderSize {
		return nil, errors.New("traverse: size embedded packet mismatch")
	}

	result.EmbeddedPacketRaw = msg.Payload[76 : 76+sizePacketEmbed]

	result.Signature = msg.Payload[76+sizePacketEmbed : 76+sizePacketEmbed+signatureSize]

	// IPv4
	ipv4B := make([]byte, 4)
	copy(ipv4B[:], msg.Payload[76+sizePacketEmbed+signatureSize:76+sizePacketEmbed+signatureSize+4])

	result.IPv4 = ipv4B
	result.PortIPv4 = binary.LittleEndian.Uint16(msg.Payload[76+sizePacketEmbed+signatureSize+4 : 76+sizePacketEmbed+signatureSize+4+2])
	result.PortIPv4ReportedExternal = binary.LittleEndian.Uint16(msg.Payload[76+sizePacketEmbed+signatureSize+6 : 76+sizePacketEmbed+signatureSize+6+2])

	// IPv6
	ipv6B := make([]byte, 16)
	copy(ipv6B[:], msg.Payload[76+sizePacketEmbed+signatureSize+8:76+sizePacketEmbed+signatureSize+8+16])

	result.IPv6 = ipv6B
	result.PortIPv6 = binary.LittleEndian.Uint16(msg.Payload[76+sizePacketEmbed+signatureSize+24 : 76+sizePacketEmbed+signatureSize+24+2])
	result.PortIPv6ReportedExternal = binary.LittleEndian.Uint16(msg.Payload[76+sizePacketEmbed+signatureSize+26 : 76+sizePacketEmbed+signatureSize+26+2])

	// TODO: Validate IPv4 and IPv6. Only external ones allowed.
	if result.IPv6.To4() != nil {
		return nil, errors.New("traverse: ipv6 address mismatch")
	}

	return result, nil
}

// EncodeTraverse encodes a traverse message
func EncodeTraverse(senderPrivateKey ed25519.PrivateKey, embeddedPacketRaw []byte, receiverEnd ed25519.PublicKey, relayPeer ed25519.PublicKey) (packetRaw []byte, err error) {
	sizePacketEmbed := len(embeddedPacketRaw)
	if isPacketSizeExceed(traversePayloadHeaderSize, sizePacketEmbed) {
		return nil, errors.New("traverse encode: embedded packet too big")
	}

	raw := make([]byte, traversePayloadHeaderSize+sizePacketEmbed)

	copy(raw[0:32], receiverEnd)
	copy(raw[32:64], relayPeer)

	expires64 := time.Now().Add(time.Hour).UTC().Unix()
	binary.LittleEndian.PutUint64(raw[64:64+8], uint64(expires64))

	binary.LittleEndian.PutUint16(raw[74:74+2], uint16(sizePacketEmbed))
	copy(raw[76:76+sizePacketEmbed], embeddedPacketRaw)

	// add signature
	signature := ed25519.Sign(senderPrivateKey, raw[:76+sizePacketEmbed])
	copy(raw[76+sizePacketEmbed:76+sizePacketEmbed+signatureSize], signature)

	// IP and ports are to be filled by authorized relay peer

	return raw, nil
}

// VerifySignature verifies the signature of the traverse message against the public key that signed it. It must be the sender of the embedded packet.
func (msg *MessageTraverse) VerifySignature(signerPublicKey ed25519.PublicKey) bool {
	return ed25519.Verify(signerPublicKey, msg.Payload[:76+len(msg.EmbeddedPacketRaw)], msg.Signature)
}

// EncodeTraverseSetAddress sets the IP and Port in a traverse message that shall be forwarded to another peer
func EncodeTraverseSetAddress(raw []byte, IPv4 net.IP, PortIPv4, PortIPv4ReportedExternal uint16, IPv6 net.IP, PortIPv6, PortIPv6ReportedExternal uint16) (err error) {
	if isPacketSizeExceed(len(raw), 0) {
		return errors.New("traverse encode 2: embedded packet too big")
	} else if len(raw) < traversePayloadHeaderSize {
		return errors.New("traverse encode 2: invalid packet")
	}

	sizePacketEmbed := binary.LittleEndian.Uint16(raw[74 : 74+2])
	if int(sizePacketEmbed) != len(raw)-traversePayloadHeaderSize {
		return errors.New("traverse encode 2: size embedded packet mismatch")
	}

	// IPv4
	if IPv4 != nil && len(IPv4) == net.IPv4len {
		copy(raw[76+sizePacketEmbed+signatureSize:76+sizePacketEmbed+signatureSize+4], IPv4.To4())
		binary.LittleEndian.PutUint16(raw[76+sizePacketEmbed+signatureSize+4:76+sizePacketEmbed+signatureSize+4+2], PortIPv4)
		binary.LittleEndian.PutUint16(raw[76+sizePacketEmbed+signatureSize+6:76+sizePacketEmbed+signatureSize+6+2], PortIPv4ReportedExternal)
	}

	// IPv6
	if IPv6 != nil && len(IPv6) == net.IPv6len {
		copy(raw[76+sizePacketEmbed+signatureSize+8:76+sizePacketEmbed+signatureSize+8+16], IPv6.To16())
		binary.LittleEndian.PutUint16(raw[76+sizePacketEmbed+signatureSize+24:76+sizePacketEmbed+signatureSize+24+2], PortIPv6)
		binary.LittleEndian.PutUint16(raw[76+sizePacketEmbed+signatureSize+26:76+sizePacketEmbed+signatureSize+26+2], PortIPv6ReportedExternal)
	}

	return nil
}
