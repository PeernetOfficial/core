/*
File Name:  Transfer Virtual Connection.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

This file defines a virtual connection between a transfer protocol and Peernet messages.
If either the downstream transfer protocol or upstream Peernet messages indicate termination, the virtual connection ceases to exist.
*/

package core

import (
	"sync"

	"github.com/PeernetOfficial/core/protocol"
)

// virtualPacketConn is a virtual connection.
type virtualPacketConn struct {
	peer *PeerInfo

	// Transfer settings
	transferProtocol uint8  // 0 = UDT
	hash             []byte // The requested hash.
	offset           uint64 // Offset to start reading.
	limit            uint64 // Limit of bytes to read at the offset.

	// Sequence number from the first outgoing or incoming packet.
	sequenceNumber uint32

	// data channel
	incomingData chan []byte
	outgoingData chan []byte

	// internal data
	closed        bool
	terminateChan chan struct{}
	reason        int // Reason why it was closed
	sync.Mutex
}

// newVirtualPacketConn creates a new virtual connection (both incomign and outgoing).
func newVirtualPacketConn(peer *PeerInfo, protocol uint8, hash []byte, offset, limit uint64) (v *virtualPacketConn) {
	v = &virtualPacketConn{
		peer:             peer,
		transferProtocol: protocol,
		hash:             hash,
		offset:           offset,
		limit:            limit,
		incomingData:     make(chan []byte, 100),
		outgoingData:     make(chan []byte),
		terminateChan:    make(chan struct{}),
	}

	go v.writeForward()

	return
}

// writeForward forwards outgoing messages
func (v *virtualPacketConn) writeForward() {
	for {
		select {
		case data := <-v.outgoingData:
			v.peer.sendTransfer(data, protocol.TransferControlActive, v.transferProtocol, v.hash, v.offset, v.limit, v.sequenceNumber)

		case <-v.terminateChan:
			return
		}
	}
}

// receiveData receives incoming data via an external message. Blocking until a read occurs or the connection is terminated!
func (v *virtualPacketConn) receiveData(data []byte) {
	if v.IsTerminated() {
		return
	}

	// pass the data on
	select {
	case v.incomingData <- data:
	case <-v.terminateChan:
	}
}

// Terminate closes the connection. Do not call this function manually. Use the underlying protocol's function to close the connection.
// Reason: 404 = Remote peer does not store file (upstream), 1 = Transfer Protocol indicated closing (downstream), 2 = Remote termination signal (upstream), 3 = Sequence invalidation or expiration (upstream)
func (v *virtualPacketConn) Terminate(reason int) (err error) {
	v.Lock()
	defer v.Unlock()

	if v.closed { // if already closed, take no action
		return
	}

	v.closed = true
	v.reason = reason
	close(v.terminateChan)

	return
}

// IsTerminated checks if the connection is terminated
func (v *virtualPacketConn) IsTerminated() bool {
	return v.closed
}

// sequenceTerminate is a wrapper for sequenece termination (invalidation or expiration)
func (v *virtualPacketConn) sequenceTerminate() {
	v.Terminate(3)
}

// Close provides a Close function to be called by the underlying transfer protocol.
// Do not call the function manually; otherwise the underlying transfer protocol may not have time to send a termination message (and the remote peer would subsequently try to reconnect).
func (v *virtualPacketConn) Close() (err error) {
	networks.Sequences.InvalidateSequence(v.peer.PublicKey, v.sequenceNumber, true)
	return v.Terminate(1)
}
