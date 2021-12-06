/*
File Name:  Message Encoding Transfer.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Transfer message encoding:
Offset  Size   Info
0       1      Control
1       1      Transfer Protocol
2       32     File Hash

Control = 0: Request Start
34      8      Offset to start reading in the file
42      8      Limit of bytes to read at the offset

Offset + limit must not exceed the file size.

Control = 3: Active
34      ?      Embedded protocol data

*/

package protocol

import (
	"encoding/binary"
	"errors"

	"github.com/PeernetOfficial/core/btcec"
)

// MessageTransfer is the decoded transfer message.
// It is sent to initiate a file transfer, and to send data as part of a file transfer. The actual file data is encapsulated via UDT.
type MessageTransfer struct {
	*MessageRaw             // Underlying raw message.
	Control          uint8  // Control. See TransferControlX.
	TransferProtocol uint8  // Embedded transfer protocol: 0 = UDT
	Hash             []byte // Hash of the file to transfer.
	Offset           uint64 // Offset to start reading at. Only TransferControlRequestStart.
	Limit            uint64 // Limit (count of bytes) to read starting at the offset. Only TransferControlRequestStart.
	Data             []byte // Embedded protocol data. Only TransferControlActive.
}

const (
	TransferControlRequestStart = 0 // Request start transfer of file. Data at byte 34 is offset and limit to read, each 8 bytes. Limit may be 0 to indicate entire file.
	TransferControlNotAvailable = 1 // Requested file not available
	TransferControlActive       = 2 // Active file transfer
	TransferControlTerminate    = 3 // Terminate
)

const TransferProtocolUDT = 0 // Indicates that UDT is used as embedded transfer protocol.

const transferPayloadHeaderSize = 34

// DecodeTransfer decodes a transfer message
func DecodeTransfer(msg *MessageRaw) (result *MessageTransfer, err error) {
	if len(msg.Payload) < transferPayloadHeaderSize {
		return nil, errors.New("transfer: invalid minimum length")
	}

	result = &MessageTransfer{
		MessageRaw: msg,
		Hash:       make([]byte, HashSize),
	}

	result.Control = msg.Payload[0]
	result.TransferProtocol = msg.Payload[1]
	copy(result.Hash, msg.Payload[2:2+HashSize])

	switch result.Control {
	case TransferControlRequestStart:
		// Offset and Limit must be provided after the header.
		if len(msg.Payload) < transferPayloadHeaderSize+16 {
			return nil, errors.New("transfer: invalid minimum length")
		}

		result.Offset = binary.LittleEndian.Uint64(msg.Payload[34 : 34+8])
		result.Limit = binary.LittleEndian.Uint64(msg.Payload[42 : 42+8])

	case TransferControlActive:
		result.Data = msg.Payload[34:]

	}

	return result, nil
}

// TransferMaxEmbedSize is a recommended default upper size of embedded data inside the Transfer message, to be used as MaxPacketSize limit in the embedded protocol.
// This value is chosen as the lowest denominator of different environments (IPv4, IPv6, Ethernet, Internet) for safe transfer, not for highest performance.
// The caller may send bigger payloads but may risk that data packets are simply dropped and never arrive. A MTU negotiation or detection could pimp that.
const TransferMaxEmbedSize = internetSafeMTU - PacketLengthMin - transferPayloadHeaderSize

// EncodeTransfer encodes a transfer message. The embedded packet size must be smaller than TransferMaxEmbedSize.
func EncodeTransfer(senderPrivateKey *btcec.PrivateKey, data []byte, control, transferProtocol uint8, hash []byte, offset, limit uint64) (packetRaw []byte, err error) {
	if control == TransferControlRequestStart && len(data) != 0 {
		return nil, errors.New("transfer encode: payload not allowed in start")
	} else if isPacketSizeExceed(transferPayloadHeaderSize, len(data)) {
		return nil, errors.New("transfer encode: embedded packet too big")
	}

	packetSize := transferPayloadHeaderSize
	if control == TransferControlRequestStart {
		packetSize += 16
	} else if control == TransferControlActive {
		packetSize += len(data)
	}

	raw := make([]byte, packetSize)

	raw[0] = control
	raw[1] = transferProtocol
	copy(raw[2:2+HashSize], hash)

	if control == TransferControlRequestStart {
		binary.LittleEndian.PutUint64(raw[34:34+8], offset)
		binary.LittleEndian.PutUint64(raw[42:42+8], limit)
	} else if control == TransferControlActive {
		copy(raw[34:34+len(data)], data)
	}

	return raw, nil
}

// IsLast checks if the incoming message is the last one in this transfer.
func (msg *MessageTransfer) IsLast() bool {
	return msg.Control == TransferControlTerminate || msg.Control == TransferControlNotAvailable
}
