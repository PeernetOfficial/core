/*
File Name:  Transfer UDT.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Each transfer over UDT starts with a header:
Offset  Size   Info
0       8      Total File Size.
8       8      Transfer Size.

TODO: Add timeouts for listening and sending.

*/

package core

import (
	"encoding/binary"
	"errors"
	"net"
	"time"

	"github.com/PeernetOfficial/core/protocol"
	"github.com/PeernetOfficial/core/udt"
)

// transferSequenceTimeout is the timeout for a follow-up message to appear, otherwise the transfer will be terminated.
var transferSequenceTimeout = time.Minute * 10

// startFileTransferUDT starts a file transfer from the local warehouse to the remote peer.
// It creates a virtual UDT client to transfer data to a remote peer. Counterintuitively, this will be the "file server" peer.
func (peer *PeerInfo) startFileTransferUDT(hash []byte, fileSize uint64, offset, limit uint64, sequenceNumber uint32) (err error) {
	if limit > 0 && offset+limit > fileSize {
		return errors.New("invalid limit")
	} else if offset > fileSize {
		return errors.New("invalid offset")
	} else if limit == 0 {
		limit = fileSize - offset
	}

	virtualConnection := newVirtualPacketConn(peer, 0, hash, offset, limit)

	// register the sequence since packets are sent bi-directional
	virtualConnection.sequenceNumber = sequenceNumber
	networks.Sequences.RegisterSequenceBi(peer.PublicKey, sequenceNumber, virtualConnection, transferSequenceTimeout, virtualConnection.sequenceTerminate)

	// start UDT sender
	udtConn, err := udt.DialUDT(udt.DefaultConfig(), virtualConnection, virtualConnection.incomingData, virtualConnection.outgoingData, virtualConnection.terminateChan, true)
	if err != nil {
		return err
	}

	defer udtConn.Close() // warning: This is currently blocking in case the other side does not call Close().

	// Start by sending the header: Total File Size and Transfer Size.
	header := make([]byte, 16)
	binary.LittleEndian.PutUint64(header[0:8], fileSize)
	binary.LittleEndian.PutUint64(header[8:16], limit-offset)
	if n, err := udtConn.Write(header); err != nil {
		return err
	} else if n != len(header) {
		return errors.New("error sending header")
	}

	_, _, err = UserWarehouse.ReadFile(hash, int64(offset), int64(limit), udtConn)

	return err
}

// FileTransferRequestUDT creates a UDT server listening for incoming data transfer and requests a file transfer from a remote peer.
func (peer *PeerInfo) FileTransferRequestUDT(hash []byte, offset, limit uint64) (udtConn net.Conn, err error) {
	virtualConnection := newVirtualPacketConn(peer, 0, hash, offset, limit)

	// new sequence
	sequence := networks.Sequences.NewSequenceBi(peer.PublicKey, &peer.messageSequence, virtualConnection, transferSequenceTimeout, virtualConnection.sequenceTerminate)
	if sequence == nil {
		return nil, errors.New("cannot acquire sequence")
	}
	virtualConnection.sequenceNumber = sequence.SequenceNumber

	// start UDT receiver
	udtListener := udt.ListenUDT(udt.DefaultConfig(), virtualConnection, virtualConnection.incomingData, virtualConnection.outgoingData, virtualConnection.terminateChan)

	// request file transfer
	peer.sendTransfer(nil, protocol.TransferControlRequestStart, virtualConnection.transferProtocol, hash, offset, limit, virtualConnection.sequenceNumber)

	// accept the connection
	udtConn, err = udtListener.Accept()
	if err != nil {
		udtListener.Close()
		return nil, err
	}

	// We do not close the UDT listener here. It should automatically close after udtConn is closed.

	return udtConn, nil
}

// FileTransferReadHeaderUDT starts reading a file via UDT. It will only read the header and keeps the connection open.
func FileTransferReadHeaderUDT(udtConn net.Conn) (fileSize, transferSize uint64, err error) {
	// read the header
	header := make([]byte, 16)
	if n, err := udtConn.Read(header); err != nil {
		return 0, 0, err
	} else if n != len(header) {
		return 0, 0, errors.New("error reading header")
	}

	fileSize = binary.LittleEndian.Uint64(header[0:8])
	transferSize = binary.LittleEndian.Uint64(header[8:16])

	return fileSize, transferSize, nil
}
