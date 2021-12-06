/*
File Name:  Message Encoding Get Block.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Get Block message encoding:
Offset  Size    Info
0       1       Control
1       33      Peer ID compressed form identifying which blockchain to transfer

Control = 0: Request Blocks
34      8       Limit total count of blocks to transfer. The transfer will be terminated if the limit is reached.
42      8       Limit of bytes per block to transfer max. Blocks exceeding this limit will not be transferred.
50      2       Count of block ranges
52      16 * ?  List of block ranges

Block range:
0       8       Block number
8       8       Count of blocks

Control = 3: Active
34      ?       Embedded block data as stream.

For the block stream there is a header preceding each block:
Offset  Size    Info
0       1       Availability
                0 = Block range is available.
                1 = Block range not available.
                2 = Block range exceeds size limit.
1       16      Block range
17      8       Block size
*/

package protocol

import (
	"encoding/binary"
	"errors"
	"io"

	"github.com/PeernetOfficial/core/btcec"
)

const (
	GetBlockControlRequestStart = 0 // Request start transfer of blocks
	GetBlockControlNotAvailable = 1 // Requested blockchain not available (not found)
	GetBlockControlActive       = 2 // Active block transfer
	GetBlockControlTerminate    = 3 // Terminate
	GetBlockControlEmpty        = 4 // Requested blockchain has 0 blocks
)

const (
	GetBlockStatusAvailable    = 0
	GetBlockStatusNotAvailable = 1
	GetBlockStatusSizeExceed   = 2
)

// MessageGetBlock is the decoded Get Block message.
type MessageGetBlock struct {
	*MessageRaw                          // Underlying raw message.
	Control             uint8            // Control. See TransferControlX.
	BlockchainPublicKey *btcec.PublicKey // Peer ID of blockchain to transfer.

	// fields valid only for GetBlockControlRequestStart
	LimitBlockCount uint64       // Limit total count of blocks to transfer
	MaxBlockSize    uint64       // Limit of bytes per block to transfer max. Blocks exceeding this limit will not be transferred.
	TargetBlocks    []BlockRange // Target list of block ranges to transfer.

	// fields valid only for GetBlockControlActive
	Data []byte // Embedded protocol data.
}

// BlockRange is a single start-count range.
type BlockRange struct {
	Offset uint64 // Block number start
	Limit  uint64 // Count of blocks
}

// DecodeGetBlock decodes a Get Block message
func DecodeGetBlock(msg *MessageRaw) (result *MessageGetBlock, err error) {
	if len(msg.Payload) < 34 {
		return nil, errors.New("get block: invalid minimum length")
	}

	result = &MessageGetBlock{
		MessageRaw: msg,
	}

	result.Control = msg.Payload[0]

	peerIDcompressed := msg.Payload[1:34]
	if result.BlockchainPublicKey, err = btcec.ParsePubKey(peerIDcompressed, btcec.S256()); err != nil {
		return nil, err
	}

	if result.Control == GetBlockControlRequestStart {
		if len(msg.Payload) < 52 {
			return nil, errors.New("get block: invalid minimum length")
		}

		result.LimitBlockCount = binary.LittleEndian.Uint64(msg.Payload[34 : 34+8])
		result.MaxBlockSize = binary.LittleEndian.Uint64(msg.Payload[42 : 42+8])

		countBlockRanges := int(binary.LittleEndian.Uint16(msg.Payload[50:52]))
		if countBlockRanges == 0 {
			return nil, errors.New("get block: empty block range")
		} else if len(msg.Payload) < 52+16*countBlockRanges {
			return nil, errors.New("get block: cound block ranges exceeds length")
		}

		index := 52

		for n := 0; n < countBlockRanges; n++ {
			var target BlockRange
			target.Offset = binary.LittleEndian.Uint64(msg.Payload[index : index+8])
			target.Limit = binary.LittleEndian.Uint64(msg.Payload[index+8 : index+16])
			result.TargetBlocks = append(result.TargetBlocks, target)

			index += 16
		}
	} else if result.Control == GetBlockControlActive {
		result.Data = msg.Payload[34:]
	}

	return result, nil
}

// EncodeGetBlock encodes a Get Block message. The embedded packet size must be smaller than TransferMaxEmbedSize.
func EncodeGetBlock(senderPrivateKey *btcec.PrivateKey, data []byte, control uint8, blockchainPublicKey *btcec.PublicKey, limitBlockCount, maxBlockSize uint64, targetBlocks []BlockRange) (packetRaw []byte, err error) {
	if control == GetBlockControlRequestStart && len(data) != 0 {
		return nil, errors.New("get block encode: payload not allowed in start")
	} else if isPacketSizeExceed(transferPayloadHeaderSize, len(data)) {
		return nil, errors.New("get block encode: embedded packet too big")
	} else if control == GetBlockControlRequestStart && isPacketSizeExceed(52, len(targetBlocks)*16) {
		return nil, errors.New("get block encode: too many target block ranges")
	}

	packetSize := transferPayloadHeaderSize
	if control == GetBlockControlRequestStart {
		packetSize = 52 + len(targetBlocks)*16
	} else if control == GetBlockControlActive {
		packetSize += len(data)
	}

	raw := make([]byte, packetSize)

	raw[0] = control
	targetPeerID := blockchainPublicKey.SerializeCompressed()
	copy(raw[1:34], targetPeerID)

	if control == GetBlockControlRequestStart {
		binary.LittleEndian.PutUint64(raw[34:34+8], limitBlockCount)
		binary.LittleEndian.PutUint64(raw[42:42+8], maxBlockSize)
		binary.LittleEndian.PutUint16(raw[50:50+2], uint16(len(targetBlocks)))

		index := 52
		for _, target := range targetBlocks {
			binary.LittleEndian.PutUint64(raw[index:index+8], target.Offset)
			binary.LittleEndian.PutUint64(raw[index+8:index+16], target.Limit)

			index += 16
		}
	} else if control == GetBlockControlActive {
		copy(raw[34:34+len(data)], data)
	}

	return raw, nil
}

// IsLast checks if the incoming message is the last one in this transfer.
func (msg *MessageGetBlock) IsLast() bool {
	return msg.Control == GetBlockControlTerminate || msg.Control == GetBlockControlNotAvailable || msg.Control == GetBlockControlEmpty
}

// BlockTransferWriteHeader starts writing the header for a block transfer.
func BlockTransferWriteHeader(writer io.Writer, availability uint8, targetBlock BlockRange, blockSize uint64) (err error) {
	header := make([]byte, 25)
	header[0] = availability
	binary.LittleEndian.PutUint64(header[1:9], targetBlock.Offset)
	binary.LittleEndian.PutUint64(header[9:17], targetBlock.Limit)
	binary.LittleEndian.PutUint64(header[17:25], blockSize)

	_, err = writer.Write(header)
	return err
}

// BlockTransferReadBlock reads the header and the block from the reader
func BlockTransferReadBlock(reader io.Reader, maxBlockSize uint64) (data []byte, targetBlock BlockRange, blockSize uint64, availability uint8, err error) {
	header := make([]byte, 25)

	if _, err := io.ReadAtLeast(reader, header, len(header)); err != nil {
		return nil, targetBlock, 0, 0, err
	}

	availability = header[0]
	targetBlock.Offset = binary.LittleEndian.Uint64(header[1:9])
	targetBlock.Limit = binary.LittleEndian.Uint64(header[9:17])
	blockSize = binary.LittleEndian.Uint64(header[17:25])

	if availability != GetBlockStatusAvailable { // return if status isn't available
		return nil, targetBlock, blockSize, availability, nil
	}

	if blockSize > maxBlockSize {
		return nil, targetBlock, blockSize, availability, errors.New("remote block size exceeds limit")
	}

	// read the block
	block := make([]byte, blockSize)

	if _, err := io.ReadAtLeast(reader, block, len(block)); err != nil {
		return nil, targetBlock, blockSize, availability, err
	}

	return block, targetBlock, blockSize, availability, nil
}
