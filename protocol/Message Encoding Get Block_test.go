/*
File Name:  Message Encoding Get Block_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package protocol

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	mathrand "math/rand"
	"testing"
	"uuid"
)

// TestGetBlockRequest checks the bytes of a Get Block message that requests blocks, and that decoding returns its fields:
// the control, the Peer ID of the blockchain, the version of the blockchain, the limit of blocks, the max block size, the transfer ID, the count of block ranges and the block ranges.
func TestGetBlockRequest(t *testing.T) {
	publicKey := make([]byte, 32)
	rand.Read(publicKey)

	var transferID uuid.UUID
	rand.Read(transferID[:])

	version, limitBlockCount, maxBlockSize := mathrand.Uint64(), mathrand.Uint64(), mathrand.Uint64()

	targets := make([]BlockRange, 1+mathrand.Intn(5))
	for n := range targets {
		targets[n] = BlockRange{Offset: mathrand.Uint64(), Limit: mathrand.Uint64()}
	}
	t.Logf("version %d, limit %d, max block size %d, %d block ranges", version, limitBlockCount, maxBlockSize, len(targets))

	expected := []byte{GetBlockControlRequestStart}
	expected = append(expected, publicKey...)
	expected = binary.LittleEndian.AppendUint64(expected, version)
	expected = binary.LittleEndian.AppendUint64(expected, limitBlockCount)
	expected = binary.LittleEndian.AppendUint64(expected, maxBlockSize)
	expected = append(expected, transferID[:]...)
	expected = binary.LittleEndian.AppendUint16(expected, uint16(len(targets)))
	for _, target := range targets {
		expected = binary.LittleEndian.AppendUint64(expected, target.Offset)
		expected = binary.LittleEndian.AppendUint64(expected, target.Limit)
	}

	raw, err := EncodeGetBlock(nil, nil, GetBlockControlRequestStart, publicKey, version, limitBlockCount, maxBlockSize, targets, transferID)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	} else if len(raw) != 75+16*len(targets) {
		t.Errorf("message of %d bytes, expected %d", len(raw), 75+16*len(targets))
	}
	if !bytes.Equal(raw, expected) {
		t.Fatalf("message is\n%x, expected\n%x", raw, expected)
	}

	msg, err := DecodeGetBlock(&MessageRaw{PacketRaw: PacketRaw{Payload: raw}})
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if msg.Control != GetBlockControlRequestStart || !bytes.Equal(msg.BlockchainPublicKey, publicKey) || msg.BlockchainVersion != version || msg.LimitBlockCount != limitBlockCount || msg.MaxBlockSize != maxBlockSize || msg.TransferID != transferID {
		t.Errorf("decoded control %d, Peer ID %x, version %d, limit %d, max block size %d, transfer ID %x", msg.Control, msg.BlockchainPublicKey, msg.BlockchainVersion, msg.LimitBlockCount, msg.MaxBlockSize, msg.TransferID[:])
	}
	if len(msg.TargetBlocks) != len(targets) {
		t.Fatalf("decoded %d block ranges, expected %d", len(msg.TargetBlocks), len(targets))
	}
	for n := range targets {
		if msg.TargetBlocks[n] != targets[n] {
			t.Errorf("decoded block range %d is %d count %d, expected %d count %d", n, msg.TargetBlocks[n].Offset, msg.TargetBlocks[n].Limit, targets[n].Offset, targets[n].Limit)
		}
	}

	// A message that ends before its last block range, or before the count of block ranges, is not decoded.
	for _, size := range []int{len(raw) - 1, 75 - 1} {
		if _, err := DecodeGetBlock(&MessageRaw{PacketRaw: PacketRaw{Payload: raw[:size:size]}}); err == nil {
			t.Errorf("message cut to %d bytes: no error returned", size)
		}
	}
}
