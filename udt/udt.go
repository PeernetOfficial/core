package udt

/*
Package udt provides a pure Go implementation of the UDT protocol per
http://udt.sourceforge.net/doc/draft-gg-udt-03.txt.

udt does not implement all of the spec.  In particular, the following are not
implemented:

- STREAM mode (only UDP is supported)

*/

import (
	"io"
	"net"
)

// DialUDT establishes an outbound UDT connection using the existing provided packet connection. It creates a UDT client.
func DialUDT(config *Config, closer io.Closer, incomingData <-chan []byte, outgoingData chan<- []byte, terminationSignal <-chan struct{}, isStream bool) (net.Conn, error) {
	m := newMultiplexer(closer, config.MaxPacketSize, incomingData, outgoingData, terminationSignal)

	s := m.newSocket(config, false, !isStream)
	err := s.startConnect()

	return s, err
}
