/*
File Name:  Message Encoding.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Intermediary between low-level packets and high-level interpretation.
*/

package protocol

import (
	"github.com/btcsuite/btcd/btcec"
)

// ProtocolVersion is the current protocol version
const ProtocolVersion = 0

// MessageRaw is a high-level message between peers that has not been decoded
type MessageRaw struct {
	PacketRaw
	SenderPublicKey *btcec.PublicKey // Sender Public Key, ECDSA (secp256k1) 257-bit
	SequenceInfo    *SequenceExpiry  // Sequence
}

// The maximum packet size is 65507 bytes = 65535 - 8 UDP byte header - 20 byte IP header.
// However, due to the MTU soft limit and fragmentation, packets should be as small as possible.
const udpMaxPacketSize = 65507

// isPacketSizeExceed checks if the max packet size would be exceeded with the payload
func isPacketSizeExceed(currentSize int, testSize int) bool {
	return currentSize+testSize > udpMaxPacketSize-PacketLengthMin
}
