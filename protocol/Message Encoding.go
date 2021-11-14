/*
File Name:  Message Encoding.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Intermediary between low-level packets and high-level interpretation.
*/

package protocol

import (
	"github.com/PeernetOfficial/core/btcec"
)

// ProtocolVersion is the current protocol version
const ProtocolVersion = 0

// MessageRaw is a high-level message between peers that has not been decoded
type MessageRaw struct {
	PacketRaw
	SenderPublicKey *btcec.PublicKey // Sender Public Key, ECDSA (secp256k1) 257-bit
	SequenceInfo    *SequenceExpiry  // Sequence
}

// The maximum packet size is = 65535 - 8 UDP byte header - 40 byte IPv6 header (IPv4 header is only 20 bytes).
// However, due to the MTU soft limit and fragmentation, packets should be as small as possible.
const udpMaxPacketSize = 65535 - 8 - 40

// internetSafeMTU is a value relatively safe to use for transmitting over the internet
// Theory: The value is different for IPv4 (min 576 bytes, Ethernet 1500 bytes) and IPv6 (min 1280 bytes). 8 byte UDP header must be subtracted, as well as the IP header (20 bytes for IPv4, 40 for IPv6).
// One simple test during development showed that 1500 - 8 - 40 - 8 worked for file transfer over IPv6 in Prague.
// For IPv6 the internet recommends the minimal possible value: 1280 bytes.
// This will be good enough for now. MTU negotiation that deviates from this value can be implemented separately (for example as part of file transfer).
// Since packets may be sent at anytime via IPv4/IPv6 connections (even concurrently on multiple), there is a single MTU value here.
const internetSafeMTU = 1280 - 8 - 40

// isPacketSizeExceed checks if the max packet size would be exceeded with the payload
func isPacketSizeExceed(currentSize int, testSize int) bool {
	return currentSize+testSize > udpMaxPacketSize-PacketLengthMin
}
