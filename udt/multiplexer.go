// Note: The multiplexer is no longer a multiplexer. Before, it tried send out future UDT traffic over an old (invalidated) PacketConn.

package udt

import (
	"fmt"
	"math/rand"
	"net"
	"sync"

	"github.com/PeernetOfficial/core/udt/packet"
)

// A multiplexer is a single UDT socket over a single PacketConn.
type multiplexer struct {
	conn       net.PacketConn     // the UDPConn from which we read/write
	socket     *udtSocket         // Socket
	socketID   uint32             // Socket ID
	listenSock *listener          // the server socket listening to incoming connections, if there is one. Set by caller.
	mtu        uint               // the Maximum Transmission Unit of packets sent from this address
	pktOut     chan packet.Packet // packets queued for immediate sending
	sync.Mutex                    // Synchronized access to socket/listenSock
}

func newMultiplexer(conn net.PacketConn, mtu uint) (m *multiplexer) {
	m = &multiplexer{
		conn:   conn,
		mtu:    mtu,                           // to be verified?!
		pktOut: make(chan packet.Packet, 100), // todo: figure out how to size this
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

	m.conn.Close()
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

	m.conn.Close()
	close(m.pktOut)
}

// read runs in a goroutine and reads packets from conn using a buffer from the readBufferPool, or a new buffer.
func (m *multiplexer) goRead() {
	buf := make([]byte, m.mtu)
	for {
		numBytes, _, err := m.conn.ReadFrom(buf)
		if err != nil {
			return
		}

		p, err := packet.DecodePacket(buf[0:numBytes])
		if err != nil {
			fmt.Printf("Unable to decode packet: %s\n", err)
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
	buf := make([]byte, m.mtu)
	for pkt := range m.pktOut {
		plen, err := pkt.WriteTo(buf)
		if err != nil {
			// TODO: handle write error
			fmt.Printf("Unable to buffer out: %s\n", err.Error())
			return
		}

		if _, err = m.conn.WriteTo(buf[0:plen], nil); err != nil {
			// TODO: handle write error
			fmt.Printf("Unable to write out: %s\n", err.Error())
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
