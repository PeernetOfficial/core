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

	"github.com/google/uuid"
)

// virtualPacketConn is a virtual connection.
type virtualPacketConn struct {
	peer *PeerInfo

	// function to send data to the remote peer
	sendData func(data []byte, sequenceNumber uint32, transferID uuid.UUID)

	// Sequence number from the first outgoing or incoming packet.
	sequenceNumber uint32

	// Transfer ID represents a session ID valid only for the duration of the transfer.
	transferID uuid.UUID

	// data channel
	incomingData chan []byte
	outgoingData chan []byte

	// internal data
	closed            bool
	terminationSignal chan struct{} // The termination signal shall be used by the underlying protocol to detect upstream termination.
	reason            int           // Reason why it was closed
	sync.Mutex
}

// newVirtualPacketConn creates a new virtual connection (both incomign and outgoing).
func newVirtualPacketConn(peer *PeerInfo, sendData func(data []byte, sequenceNumber uint32, transferID uuid.UUID)) (v *virtualPacketConn) {
	v = &virtualPacketConn{
		peer:              peer,
		sendData:          sendData,
		incomingData:      make(chan []byte, 512),
		outgoingData:      make(chan []byte),
		terminationSignal: make(chan struct{}),
	}

	go v.writeForward()

	return
}

// writeForward forwards outgoing messages
func (v *virtualPacketConn) writeForward() {
	for {
		select {
		case data := <-v.outgoingData:
			v.sendData(data, v.sequenceNumber, v.transferID)

		case <-v.terminationSignal:
			return
		}
	}
}

// receiveData receives incoming data via an external message. Non-blocking.
func (v *virtualPacketConn) receiveData(data []byte) {
	if v.IsTerminated() {
		return
	}

	// pass the data on
	select {
	case v.incomingData <- data:
	case <-v.terminationSignal:
	default:
		// packet lost
	}
}

// Terminate closes the connection. Do not call this function manually. Use the underlying protocol's function to close the connection.
// Reason: 404 = Remote peer does not store file (upstream), 2 = Remote termination signal (upstream), 3 = Sequence invalidation or expiration (upstream), 1000+ = Transfer protocol indicated closing (downstream)
func (v *virtualPacketConn) Terminate(reason int) (err error) {
	v.Lock()
	defer v.Unlock()

	if v.closed { // if already closed, take no action
		return
	}

	v.closed = true
	v.reason = reason
	close(v.terminationSignal)

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
// Rather, use the underlying transfer protocol's close function.
func (v *virtualPacketConn) Close(reason int) (err error) {
	v.peer.Backend.networks.Sequences.InvalidateSequence(v.peer.PublicKey, v.sequenceNumber, true)
	return v.Terminate(reason)
}

// CloseLinger is to be called by the underlying transfer protocol when it will close the socket soon after lingering around.
// Lingering happens to resend packets at the end of transfer, when it is not immediately known whether the remote peer received all packets.
func (v *virtualPacketConn) CloseLinger(reason int) (err error) {
	v.reason = reason
	return nil
}

// GetTerminateReason returns the termination reason. 0 = Not yet terminated.
func (v *virtualPacketConn) GetTerminateReason() int {
	return v.reason
}
