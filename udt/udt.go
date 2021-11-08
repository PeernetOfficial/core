package udt

/*
Package udt provides a pure Go implementation of the UDT protocol per
http://udt.sourceforge.net/doc/draft-gg-udt-03.txt.

udt does not implement all of the spec.  In particular, the following are not
implemented:

- STREAM mode (only UDP is supported)

*/

import (
	"net"
)

// Closer provides a status code indicating why the closing happens.
type Closer interface {
	Close(reason int) error       // Close is called when the socket is actually closed.
	CloseLinger(reason int) error // CloseLinger is called when the socket indicates to be closed soon, after the linger time.
}

// The termination reason is passed on to the close function
const (
	TerminateReasonListenerClosed        = 1000 // Listener: The listener.Close function was called.
	TerminateReasonLingerTimerExpired    = 1001 // Socket: The linger timer expired. Use CloseLinger to know the actual closing reason.
	TerminateReasonConnectTimeout        = 1002 // Socket: The connection timed out when sending the initial handshake.
	TerminateReasonRemoteSentShutdown    = 1003 // Remote peer sent a shutdown message.
	TerminateReasonCannotProcessOutgoing = 1004 // Send: Cannot process outgoing messages.
	TerminateReasonInvalidPacketIDAck    = 1005 // Send: Invalid packet ID received in ACK message.
	TerminateReasonInvalidPacketIDNak    = 1006 // Send: Invalid packet ID received in NAK message.
	TerminateReasonCorruptPacketNak      = 1007 // Send: Invalid NAK packet received.
	TerminateReasonExpireTimer           = 1008 // Send: EXP timer expired.
)

// DialUDT establishes an outbound UDT connection using the existing provided packet connection. It creates a UDT client.
func DialUDT(config *Config, closer Closer, incomingData <-chan []byte, outgoingData chan<- []byte, terminationSignal <-chan struct{}, isStream bool) (net.Conn, error) {
	m := newMultiplexer(closer, config.MaxPacketSize, incomingData, outgoingData, terminationSignal)

	s := m.newSocket(config, false, !isStream)
	err := s.startConnect()

	return s, err
}
