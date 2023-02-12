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

// VirtualPacketConn is a virtual connection.
type VirtualPacketConn struct {
	Peer *PeerInfo

	// Stats are maintained by the caller
	Stats interface{}

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

// newVirtualPacketConn creates a new virtual connection (both incoming and outgoing).
func newVirtualPacketConn(peer *PeerInfo, sendData func(data []byte, sequenceNumber uint32, transferID uuid.UUID)) (v *VirtualPacketConn) {
	v = &VirtualPacketConn{
		Peer:              peer,
		sendData:          sendData,
		incomingData:      make(chan []byte, 512),
		outgoingData:      make(chan []byte),
		terminationSignal: make(chan struct{}),
	}

	go v.writeForward()

	return
}

// writeForward forwards outgoing messages
func (v *VirtualPacketConn) writeForward() {
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
func (v *VirtualPacketConn) receiveData(data []byte) {
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
func (v *VirtualPacketConn) Terminate(reason int) (err error) {
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
func (v *VirtualPacketConn) IsTerminated() bool {
	return v.closed
}

// sequenceTerminate is a wrapper for sequenece termination (invalidation or expiration)
func (v *VirtualPacketConn) sequenceTerminate() {
	v.Terminate(3)
}

// Close provides a Close function to be called by the underlying transfer protocol.
// Do not call the function manually; otherwise the underlying transfer protocol may not have time to send a termination message (and the remote peer would subsequently try to reconnect).
// Rather, use the underlying transfer protocol's close function.
func (v *VirtualPacketConn) Close(reason int) (err error) {
	v.Peer.Backend.networks.Sequences.InvalidateSequence(v.Peer.PublicKey, v.sequenceNumber, true)
	return v.Terminate(reason)
}

// CloseLinger is to be called by the underlying transfer protocol when it will close the socket soon after lingering around.
// Lingering happens to resend packets at the end of transfer, when it is not immediately known whether the remote peer received all packets.
func (v *VirtualPacketConn) CloseLinger(reason int) (err error) {
	v.reason = reason
	return nil
}

// GetTerminateReason returns the termination reason. 0 = Not yet terminated.
func (v *VirtualPacketConn) GetTerminateReason() int {
	return v.reason
}
