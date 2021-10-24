/*
File Name:  Transfer UDT.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

The strategy is to create a virtual net.PacketConn which can be used by the UDT package for input/output.
TODO: Add timeouts for listening and sending.

*/

package core

import (
	"errors"
	"net"
	"time"

	"github.com/PeernetOfficial/core/protocol"
	"github.com/PeernetOfficial/core/udt"
)

// transferSequenceTimeout is the timeout for a follow-up message to appear, otherwise the transfer will be terminated.
var transferSequenceTimeout = time.Minute * 10

// startFileTransferUDT starts a file transfer to a remote peer.
// It creates a virtual UDT client to transfer data to a remote peer. Counterintuitively, this will be the "file server" peer.
func (peer *PeerInfo) startFileTransferUDT(hash []byte, offset, limit uint64, sequenceNumber uint32) (err error) {
	virtualConnection := newVirtualPacketConn(peer, 0, hash, offset, limit, false)

	// register the sequence since packets are sent bi-directional
	virtualConnection.sequenceNumber = sequenceNumber
	networks.Sequences.RegisterSequenceBi(peer.PublicKey, sequenceNumber, virtualConnection, transferSequenceTimeout, virtualConnection.sequenceTerminate)

	// start UDT sender
	udtConn, err := udt.DialUDT(udt.DefaultConfig(), virtualConnection, virtualConnection.incomingData, virtualConnection.outgoingData, virtualConnection.terminateChan, true)
	if err != nil {
		return err
	}

	_, err = UserWarehouse.ReadFile(hash, int64(offset), int64(limit), udtConn)

	// close the UDT client and virtual connection in any case
	udtConn.Close() // warning: This is currently blocking in case the other side does not call Close().

	return err
}

// RequestFileTransferUDT creates a UDT server listening for incoming data transfer and requests a file transfer from a remote peer.
func (peer *PeerInfo) RequestFileTransferUDT(hash []byte, offset, limit uint64) (udtConn net.Conn, err error) {
	virtualConnection := newVirtualPacketConn(peer, 0, hash, offset, limit, true)

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
