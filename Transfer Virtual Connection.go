/*
File Name:  Transfer Virtual Connection.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

This file defines a virtual net.PacketConn which sends transfer messages and can be used to embed other transfer protocols.
*/

package core

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/PeernetOfficial/core/protocol"
)

// virtualPacketConn is a virtual connection.
// The required functions are ReadFrom, WriteTo, Close, LocalAddr, SetDeadline, SetReadDeadline, SetWriteDeadline.
type virtualPacketConn struct {
	peer *PeerInfo

	// isListenMode puts this connection into listening mode (counterintuitively, a server).
	isListenMode bool

	// Transfer settings
	transferProtocol uint8  // 0 = UDT
	hash             []byte // The requested hash.
	offset           uint64 // Offset to start reading.
	limit            uint64 // Limit of bytes to read at the offset.

	// Sequence number from the first outgoing or incoming packet.
	sequenceNumber uint32

	// data channel
	incomingData chan []byte

	// internal data
	closed        bool
	terminateChan chan struct{}
	reason        int // Reason why it was closed
	sync.Mutex
}

// newVirtualPacketConn creates a new virtual connection (both incomign and outgoing).
func newVirtualPacketConn(peer *PeerInfo, protocol uint8, hash []byte, offset, limit uint64, isListen bool) (v *virtualPacketConn) {
	return &virtualPacketConn{
		transferProtocol: protocol,
		peer:             peer,
		hash:             hash,
		offset:           offset,
		limit:            limit,
		isListenMode:     isListen,
		incomingData:     make(chan []byte),
		terminateChan:    make(chan struct{}),
	}
}

// ReadFrom reads a packet from the connection, copying the payload into p. It returns the number of bytes copied into p and the return address that was on the packet.
func (v *virtualPacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	if v.closed {
		return 0, nil, errors.New("connection closed")
	}

	// The underlying transfer messages feature a message sequence timeout (via Sequences.RegisterSequenceBi and Sequences.NewSequenceBi).
	// The sequence timeout triggers the virtualPacketConn.Terminate function which closes terminateChan.
	// Therefore an additional timeout here would be redundant. Instead, a transfer-wide timeout, that closes terminateChan upon expiration, shall be used.

	select {
	case data := <-v.incomingData:
		n = copy(p, data)
	case <-v.terminateChan:
		return n, nil, io.EOF
	}

	return
}

// Write sends the data to the remote peer. It makes it possible to use virtualPacketConn as io.Writer.
// Note: If the packet size exceed the limit from protocol.EncodeTransfer, this function fails.
func (v *virtualPacketConn) Write(p []byte) (n int, err error) {
	if v.IsTerminated() {
		return 0, errors.New("connection closed")
	}

	// create a new packet
	err = v.peer.sendTransfer(p, protocol.TransferControlActive, v.transferProtocol, v.hash, v.offset, v.limit, v.sequenceNumber)
	if err == nil {
		n = len(p)
	}

	return
}

// receiveData receives incoming data via an external message. Blocking until a read occurs or the connection is terminated!
func (v *virtualPacketConn) receiveData(data []byte) (err error) {
	if v.IsTerminated() {
		return errors.New("connection closed")
	}

	// pass on the data
	select {
	case v.incomingData <- data:
	case <-v.terminateChan:
	}

	return
}

// Terminate closes the connection and optionally sends a termination message to the remote peer. Multiple termination signals have no effect.
// Reason: 404 = Remote peer does not store file, 1 = Local termination signal, 2 = Remote termination signal, 3 = Sequence invalidation or expiration
func (v *virtualPacketConn) Terminate(sendSignal bool, reason int) (err error) {
	v.Lock()
	defer v.Unlock()

	if v.closed { // if already closed, take no action
		return
	}

	v.closed = true
	v.reason = reason
	close(v.terminateChan)

	if sendSignal {
		err = v.peer.sendTransfer(nil, protocol.TransferControlTerminate, v.transferProtocol, v.hash, 0, 0, v.sequenceNumber)
	}

	return
}

// IsTerminated checks if the connection is terminated
func (v *virtualPacketConn) IsTerminated() bool {
	return v.closed
}

// sequenceTerminate is a wrapper for sequenece termination (invalidation or expiration)
func (v *virtualPacketConn) sequenceTerminate() {
	v.Terminate(false, 3)
}

// Close closes the connection.
func (v *virtualPacketConn) Close() (err error) {
	return v.Terminate(false, 1)
}

// WriteTo writes a packet with payload p to addr.  WriteTo can be made to time out and return an Error after a fixed time limit.
func (v *virtualPacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return v.Write(p)
}

// LocalAddr returns the local network address
func (v *virtualPacketConn) LocalAddr() (addr net.Addr) {
	return
}

// SetDeadline sets the read and write deadlines associated with the connection. It is equivalent to calling both SetReadDeadline and SetWriteDeadline.
func (v *virtualPacketConn) SetDeadline(t time.Time) (err error) {
	return nil
}

// SetReadDeadline sets the deadline for future ReadFrom calls and any currently-blocked ReadFrom call.
func (v *virtualPacketConn) SetReadDeadline(t time.Time) (err error) {
	return nil
}

// SetWriteDeadline sets the deadline for future WriteTo calls and any currently-blocked WriteTo call.
func (v *virtualPacketConn) SetWriteDeadline(t time.Time) (err error) {
	return nil
}
