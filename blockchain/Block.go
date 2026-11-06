/*
File Name:  Block.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Encoding of a block (it is the same stored in the database and shared in a message):
Offset  Size   Info
0       64     Signature of entire block by the blockchain owner
64      32     Hash (blake3) of last block. 0 for first one.
96      8      Blockchain version number
104     8      Block number
112     4      Size of entire block including this header
116     2      Count of records that follow

*/

package blockchain

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"time"

	"github.com/PeernetOfficial/core/protocol"
)

// Block is a single block containing a set of records (metadata).
// It has no upper size limit, although a soft limit of 64 KB - overhead is encouraged for efficiency.
type Block struct {
	OwnerPublicKey    ed25519.PublicKey // Owner Public Key
	NodeID            []byte            // Node ID of the owner (derived from the public key)
	LastBlockHash     []byte            // Hash of the last block. Blake3.
	BlockchainVersion uint64            // Blockchain version
	Number            uint64            // Block number
	RecordsRaw        []BlockRecordRaw  // Block records raw
}

// BlockRecordRaw is a single block record (not decoded)
type BlockRecordRaw struct {
	Type uint8     // Record Type. See RecordTypeX.
	Date time.Time // Date created. This remains the same in case of block refactoring.
	Data []byte    // Data according to the type
}

const blockHeaderSize = 118
const blockRecordHeaderSize = 13

// decodeBlock decodes a single block and verifies its signature against the blockchain owner's public key.
func decodeBlock(raw []byte, ownerPublicKey ed25519.PublicKey) (block *Block, err error) {
	if len(raw) < blockHeaderSize {
		return nil, errors.New("decodeBlock invalid block size")
	}

	if !ed25519.Verify(ownerPublicKey, raw[64:], raw[0:64]) {
		return nil, errors.New("decodeBlock invalid signature")
	}

	block = &Block{OwnerPublicKey: ownerPublicKey}

	block.NodeID = protocol.PublicKey2NodeID(block.OwnerPublicKey)

	block.LastBlockHash = make([]byte, protocol.HashSize)
	copy(block.LastBlockHash, raw[64:64+protocol.HashSize])

	block.BlockchainVersion = binary.LittleEndian.Uint64(raw[96 : 96+8])
	block.Number = binary.LittleEndian.Uint64(raw[104 : 104+8])

	blockSize := binary.LittleEndian.Uint32(raw[112 : 112+4])
	if blockSize != uint32(len(raw)) {
		return nil, errors.New("decodeBlock invalid block size")
	}

	// decode on a low-level all block records
	countRecords := binary.LittleEndian.Uint16(raw[116 : 116+2])
	index := blockHeaderSize

	for n := uint16(0); n < countRecords; n++ {
		if index+blockRecordHeaderSize > len(raw) {
			return nil, errors.New("decodeBlock record exceeds block size")
		}

		recordType := raw[index]
		recordDate := int64(binary.LittleEndian.Uint64(raw[index+1 : index+9])) // Unix time int64, the number of seconds elapsed since January 1, 1970 UTC
		recordSize := binary.LittleEndian.Uint32(raw[index+9 : index+9+4])
		index += blockRecordHeaderSize

		if index+int(recordSize) > len(raw) {
			return nil, errors.New("decodeBlock record exceeds block size")
		}

		block.RecordsRaw = append(block.RecordsRaw, BlockRecordRaw{Type: recordType, Data: raw[index : index+int(recordSize)], Date: time.Unix(recordDate, 0)})

		index += int(recordSize)
	}

	return block, nil
}

func encodeBlock(block *Block, ownerPrivateKey ed25519.PrivateKey) (raw []byte, err error) {
	var buffer bytes.Buffer
	buffer.Write(make([]byte, 64)) // Signature, filled at the end

	if block.Number > 0 && len(block.LastBlockHash) != protocol.HashSize {
		return nil, errors.New("encodeBlock invalid last block hash")
	} else if block.Number == 0 { // Block 0: Empty last hash
		block.LastBlockHash = make([]byte, 32)
	}
	buffer.Write(block.LastBlockHash)

	var temp [8]byte
	binary.LittleEndian.PutUint64(temp[0:8], block.BlockchainVersion)
	buffer.Write(temp[:8])

	binary.LittleEndian.PutUint64(temp[0:8], block.Number)
	buffer.Write(temp[:8])

	buffer.Write(make([]byte, 4)) // Size of block, filled later
	buffer.Write(make([]byte, 2)) // Count of records, filled later

	// write all records
	countRecords := uint16(0)

	for _, record := range block.RecordsRaw {
		if record.Date == (time.Time{}) { // Always set date if not already set
			record.Date = time.Now()
		}

		var tempSize, tempDate [8]byte
		binary.LittleEndian.PutUint32(tempSize[0:4], uint32(len(record.Data)))
		binary.LittleEndian.PutUint64(tempDate[0:8], uint64(record.Date.UTC().Unix()))

		buffer.Write([]byte{record.Type}) // Record Type
		buffer.Write(tempDate[:8])        // Date created
		buffer.Write(tempSize[:4])        // Size of data
		buffer.Write(record.Data)         // Data

		countRecords++
	}

	// finalize the block
	raw = buffer.Bytes()
	if len(raw) < blockHeaderSize {
		return nil, errors.New("encodeBlock invalid block size")
	}

	binary.LittleEndian.PutUint32(raw[112:112+4], uint32(len(raw))) // Size of block
	binary.LittleEndian.PutUint16(raw[116:116+2], countRecords)     // Count of records

	// signature is last
	copy(raw[0:0+64], ed25519.Sign(ownerPrivateKey, raw[64:]))

	return raw, nil
}
