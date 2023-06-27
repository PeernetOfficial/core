/*
File Username:  Transfer UDT.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

TODO: Add timeouts for listening and sending.
*/

package core

import (
	"errors"
	"time"

	"github.com/PeernetOfficial/core/protocol"
	"github.com/PeernetOfficial/core/udt"
	"github.com/google/uuid"
)

// transferSequenceTimeout is the timeout for a follow-up message to appear, otherwise the transfer will be terminated.
var transferSequenceTimeout = time.Minute * 1

// maxFlowWinSize is the maximum number of unacknowledged packets to permit. A higher number means using more memory, but reduces potential overhead since it does not stop so quickly for missing packets.
// Each unacknowledged packet may store protocol.TransferMaxEmbedSize (currently 1121 bytes) payload data in memory. A too high number may impact the speed of real-time streaming in case of lost packets.
// The actual used number will be negotiated through the UDT handshake and must be a minimum of 32.
const maxFlowWinSize = 64

// Whether to use the lite protocol for transfer of data.
const transferLite = true

// startFileTransferUDT starts a file transfer from the local warehouse to the remote peer.
// It creates a virtual UDT client to transfer data to a remote peer. Counterintuitively, this will be the "file server" peer.
func (peer *PeerInfo) startFileTransferUDT(hash []byte, fileSize uint64, offset, limit uint64, sequenceNumber uint32, transferID uuid.UUID, transferProtocol uint8) (err error) {
	if limit > 0 && offset+limit > fileSize {
		return errors.New("invalid limit")
	} else if offset > fileSize {
		return errors.New("invalid offset")
	} else if limit == 0 {
		limit = fileSize - offset
	}

	virtualConn := newVirtualPacketConn(peer, func(data []byte, sequenceNumber uint32, transferID uuid.UUID) {
		peer.sendTransfer(data, protocol.TransferControlActive, 0, hash, offset, limit, sequenceNumber, transferID, transferLite)
	})
	virtualConn.Stats = &FileTransferStats{Hash: hash, Direction: DirectionOut, FileSize: fileSize, Offset: offset, Limit: limit}

	// use the transfer ID indicated by the remote peer
	// 17.01.2021: Due to using lite IDs, the sequence termination function in RegisterSequenceBi is no longer used, as data packets are only sent via lite packets.
	virtualConn.transferID = transferID
	peer.Backend.networks.LiteRouter.RegisterLiteID(transferID, virtualConn, transferSequenceTimeout, virtualConn.sequenceTerminate)

	// register the sequence since packets are sent bi-directional
	virtualConn.sequenceNumber = sequenceNumber
	peer.Backend.networks.Sequences.RegisterSequenceBi(peer.PublicKey, sequenceNumber, virtualConn, transferSequenceTimeout, nil)

	udtConfig := udt.DefaultConfig()
	udtConfig.MaxPacketSize = protocol.TransferMaxEmbedSizeLite
	udtConfig.MaxFlowWinSize = maxFlowWinSize

	// start UDT sender
	// Set streaming to true, otherwise udtSocket.Read returns the error "Message truncated" in case the reader has a smaller buffer.
	udtConn, err := udt.DialUDT(udtConfig, virtualConn, virtualConn.incomingData, virtualConn.outgoingData, virtualConn.terminationSignal, true)
	if err != nil {
		return err
	}

	defer udtConn.Close()
	virtualConn.Stats.(*FileTransferStats).UDTConn = udtConn

	// First send the header (Total File Size, Transfer Size) and then the file data.
	protocol.FileTransferWriteHeader(udtConn, fileSize, limit)

	_, _, err = peer.Backend.UserWarehouse.ReadFile(hash, int64(offset), int64(limit), udtConn)

	return err
}

// FileTransferRequestUDT creates a UDT server listening for incoming data transfer via the lite protocol and requests a file transfer from a remote peer.
// The caller must call udtConn.Close() when done. Do not use any of the closing functions of virtualConn.
// Limit is optional. 0 means the entire file.
func (peer *PeerInfo) FileTransferRequestUDT(hash []byte, offset, limit uint64) (udtConn *udt.UDTSocket, virtualConn *VirtualPacketConn, err error) {
	virtualConn = newVirtualPacketConn(peer, func(data []byte, sequenceNumber uint32, transferID uuid.UUID) {
		peer.sendTransfer(data, protocol.TransferControlActive, protocol.TransferProtocolUDT, hash, offset, limit, sequenceNumber, transferID, transferLite)
	})

	// new lite ID
	liteID := peer.Backend.networks.LiteRouter.NewLiteID(virtualConn, transferSequenceTimeout, virtualConn.sequenceTerminate)
	virtualConn.transferID = liteID.ID
	virtualConn.Stats = &FileTransferStats{Hash: hash, Direction: DirectionIn, Offset: offset, Limit: limit}

	// new sequence
	sequence := peer.Backend.networks.Sequences.NewSequenceBi(peer.PublicKey, &peer.messageSequence, virtualConn, transferSequenceTimeout, nil)
	if sequence == nil {
		return nil, nil, errors.New("cannot acquire sequence")
	}
	virtualConn.sequenceNumber = sequence.SequenceNumber

	udtConfig := udt.DefaultConfig()
	udtConfig.MaxPacketSize = protocol.TransferMaxEmbedSizeLite
	udtConfig.MaxFlowWinSize = maxFlowWinSize

	// start UDT receiver
	udtListener := udt.ListenUDT(udtConfig, virtualConn, virtualConn.incomingData, virtualConn.outgoingData, virtualConn.terminationSignal)

	// request file transfer
	peer.sendTransfer(nil, protocol.TransferControlRequestStart, protocol.TransferProtocolUDT, hash, offset, limit, virtualConn.sequenceNumber, virtualConn.transferID, false)

	// accept the connection
	udtConn, err = udtListener.Accept()
	if err != nil {
		udtListener.Close()
		return nil, nil, err
	}
	virtualConn.Stats.(*FileTransferStats).UDTConn = udtConn

	// We do not close the UDT listener here. It should automatically close after udtConn is closed.

	return udtConn, virtualConn, nil
}

type FileTransferStats struct {
	Hash      []byte         // Hash of the file to transfer
	Direction int            // Direction of the data transfer
	FileSize  uint64         // File size if known
	Offset    uint64         // Offset to start the transfer
	Limit     uint64         // Limit in bytes to transfer
	UDTConn   *udt.UDTSocket // Underlying UDT connection
}

// Transfer directions
const (
	DirectionIn  = 0
	DirectionOut = 1
	DirectionBi  = 2
)
