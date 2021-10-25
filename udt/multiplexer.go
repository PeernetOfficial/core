// Note: The multiplexer is no longer a multiplexer. Before, it tried send out future UDT traffic over an old (invalidated) PacketConn.

package udt

import (
	"fmt"
	"io"
	"math/rand"
	"sync"

	"github.com/PeernetOfficial/core/udt/packet"
)

// A multiplexer is a single UDT socket over a single PacketConn.
type multiplexer struct {
	socket            *udtSocket         // Socket
	socketID          uint32             // Socket ID
	listenSock        *listener          // the server socket listening to incoming connections, if there is one. Set by caller.
	maxPacketSize     uint               // the Maximum Transmission Unit of packets sent from this address
	pktOut            chan packet.Packet // packets queued for immediate sending
	sync.Mutex                           // Synchronized access to socket/listenSock
	incomingData      <-chan []byte      // source to read packets from
	outgoingData      chan<- []byte      // destination to send packets to
	terminationSignal <-chan struct{}    // external termination signal to watch
	closer            io.Closer          // external closer to call in case the local socket/listener closes
}

// The closer is called when the socket/listener closes. The terminationSignal is an external (upstream) signal to watch for.
func newMultiplexer(closer io.Closer, maxPacketSize uint, incomingData <-chan []byte, outgoingData chan<- []byte, terminationSignal <-chan struct{}) (m *multiplexer) {
	m = &multiplexer{
		maxPacketSize:     maxPacketSize,
		pktOut:            make(chan packet.Packet, 100),
		closer:            closer,
		incomingData:      incomingData,
		outgoingData:      outgoingData,
		terminationSignal: terminationSignal,
	}

	go m.goRead()
	go m.goWrite()

	return
}

// unlistenUDT is the closeListen equivalent
func (m *multiplexer) unlistenUDT(l *listener) {
	m.Lock()
	defer m.Unlock()

	if m.listenSock == nil {
		return
	}

	m.listenSock = nil

	m.closer.Close()
	close(m.pktOut)
}

func (m *multiplexer) newSocket(config *Config, isServer bool, isDatagram bool) (s *udtSocket) {
	m.socketID = rand.Uint32()
	m.socket = newSocket(m, config, m.socketID, isServer, isDatagram)
	return m.socket
}

func (m *multiplexer) closeSocket(sockID uint32) {
	m.Lock()
	defer m.Unlock()

	if m.socket == nil {
		return
	}

	m.socket = nil

	m.closer.Close()
	close(m.pktOut)
}

// read runs in a goroutine and reads packets from conn using a buffer from the readBufferPool, or a new buffer.
func (m *multiplexer) goRead() {
	for {
		var buf []byte
		select {
		case buf = <-m.incomingData:
		case <-m.terminationSignal:
			return
		}

		p, err := packet.DecodePacket(buf)
		if err != nil {
			fmt.Printf("Error decoding UDT packet: %s\n", err)
			return
		}

		// attempt to route the packet
		sockID := p.SocketID()
		if sockID == 0 {
			var hsPacket *packet.HandshakePacket
			var ok bool
			if hsPacket, ok = p.(*packet.HandshakePacket); !ok {
				fmt.Printf("Received non-handshake packet with destination socket = 0\n")
				return
			}

			m.Lock()
			if m.listenSock != nil {
				m.listenSock.readHandshake(m, hsPacket)
			}
			m.Unlock()
		}
		if m.socketID == sockID && m.socket != nil {
			m.socket.readPacket(m, p)
		}
	}
}

// write runs in a goroutine and writes packets to conn using a buffer from the writeBufferPool, or a new buffer.
func (m *multiplexer) goWrite() {
	for pkt := range m.pktOut {
		buf := make([]byte, m.maxPacketSize)
		plen, err := pkt.WriteTo(buf) // encode
		if err != nil {
			// TODO: handle write error
			fmt.Printf("Error encoding UDT packet: %s\n", err.Error())
			return
		}

		select {
		case m.outgoingData <- buf[0:plen]:
		case <-m.terminationSignal:
			return
		}
	}
}

func (m *multiplexer) sendPacket(destSockID uint32, ts uint32, p packet.Packet) {
	p.SetHeader(destSockID, ts)
	if destSockID == 0 {
		if _, ok := p.(*packet.HandshakePacket); !ok {
			fmt.Printf("Sending non-handshake packet with destination socket = 0\n")
			return
		}
	}
	m.pktOut <- p
}
