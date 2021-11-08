package udt

import (
	"container/heap"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/PeernetOfficial/core/udt/packet"
)

var (
	endianness = binary.BigEndian
)

/*
Listener implements the io.Listener interface for UDT.
*/
type listener struct {
	m              *multiplexer
	accept         chan *udtSocket
	closed         chan struct{}
	acceptHist     acceptSockHeap
	acceptHistProt sync.Mutex
	config         *Config
}

func (l *listener) Accept() (net.Conn, error) {
	socket, ok := <-l.accept
	if ok {
		return socket, nil
	}
	return nil, errors.New("Listener closed")
}

func (l *listener) Close() (err error) {
	a := l.accept
	c := l.closed
	l.accept = nil
	l.closed = nil
	if a == nil || c == nil {
		return errors.New("Listener closed")
	}
	close(a)
	close(c)

	l.m.closer.Close(TerminateReasonListenerClosed)
	return nil
}

func (l *listener) Addr() net.Addr {
	//return l.m.laddr
	return nil
}

// checkValidHandshake checks to see if we want to accept a new connection with this handshake.
func (l *listener) checkValidHandshake(m *multiplexer, p *packet.HandshakePacket) bool {
	return true
}

func (l *listener) rejectHandshake(m *multiplexer, hsPacket *packet.HandshakePacket) {
	//fmt.Printf("(listener) sending handshake(reject) (id=%d)\n", hsPacket.SockID)
	m.sendPacket(hsPacket.SockID, 0, &packet.HandshakePacket{
		UdtVer:   hsPacket.UdtVer,
		SockType: hsPacket.SockType,
		ReqType:  packet.HsRefused,
	})
}

func (l *listener) readHandshake(m *multiplexer, hsPacket *packet.HandshakePacket) bool {
	if hsPacket.ReqType == packet.HsRequest {
		//fmt.Printf("(listener) sending handshake(request)  (id=%d)\n", hsPacket.SockID)

		m.sendPacket(hsPacket.SockID, 0, &packet.HandshakePacket{
			UdtVer:     hsPacket.UdtVer,
			SockType:   hsPacket.SockType,
			InitPktSeq: hsPacket.InitPktSeq,
			//MaxPktSize     uint32     // maximum packet size (including UDP/IP headers)
			//MaxFlowWinSize uint32     // maximum flow window size
			ReqType: packet.HsRequest,
			// SockID = 0
		})
		return true
	}

	// Here used to be a SYNC cookie check. Not needed.

	if !l.checkValidHandshake(m, hsPacket) {
		l.rejectHandshake(m, hsPacket)
		return false
	}

	now := time.Now()
	l.acceptHistProt.Lock()
	if l.acceptHist != nil {
		replayWindow := l.config.ListenReplayWindow
		if replayWindow <= 0 {
			replayWindow = DefaultConfig().ListenReplayWindow
		}
		l.acceptHist.Prune(time.Now().Add(-replayWindow))
		s, idx := l.acceptHist.Find(hsPacket.SockID, hsPacket.InitPktSeq)
		if s != nil {
			l.acceptHist[idx].lastTouch = now
			l.acceptHistProt.Unlock()
			return s.readHandshake(m, hsPacket)
		}
	}
	l.acceptHistProt.Unlock()

	if !l.config.CanAcceptDgram && hsPacket.SockType == packet.TypeDGRAM {
		//fmt.Printf("Refusing new socket creation from listener requesting DGRAM\n")
		l.rejectHandshake(m, hsPacket)
		return false
	}
	if !l.config.CanAcceptStream && hsPacket.SockType == packet.TypeSTREAM {
		//fmt.Printf("Refusing new socket creation from listener requesting STREAM\n")
		l.rejectHandshake(m, hsPacket)
		return false
	}
	if l.config.CanAccept != nil {
		err := l.config.CanAccept(hsPacket)
		if err != nil {
			//fmt.Printf("New socket creation from listener rejected by config: %s\n", err.Error())
			l.rejectHandshake(m, hsPacket)
			return false
		}
	}

	s := l.m.newSocket(l.config, true, hsPacket.SockType == packet.TypeDGRAM)
	l.acceptHistProt.Lock()
	if l.acceptHist == nil {
		l.acceptHist = []acceptSockInfo{{
			sockID:    hsPacket.SockID,
			initSeqNo: hsPacket.InitPktSeq,
			lastTouch: now,
			sock:      s,
		}}
		heap.Init(&l.acceptHist)
	} else {
		heap.Push(&l.acceptHist, acceptSockInfo{
			sockID:    hsPacket.SockID,
			initSeqNo: hsPacket.InitPktSeq,
			lastTouch: now,
			sock:      s,
		})
	}
	l.acceptHistProt.Unlock()
	if !s.checkValidHandshake(m, hsPacket) {
		l.rejectHandshake(m, hsPacket)
		return false
	}
	if !s.readHandshake(m, hsPacket) {
		l.rejectHandshake(m, hsPacket)
		return false
	}

	l.accept <- s
	return true
}

// ListenUDT listens for incoming UDT connections using the existing provided packet connection. It creates a UDT server.
func ListenUDT(config *Config, closer Closer, incomingData <-chan []byte, outgoingData chan<- []byte, terminationSignal <-chan struct{}) net.Listener {
	m := newMultiplexer(closer, config.MaxPacketSize, incomingData, outgoingData, terminationSignal)

	l := &listener{
		m:      m,
		accept: make(chan *udtSocket, 100),
		closed: make(chan struct{}, 1),
		config: config,
	}

	m.listenSock = l

	return l
}
