package udt

import (
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/PeernetOfficial/core/udt/packet"
)

type sockState int

const (
	sockStateInit       sockState = iota // object is being constructed
	sockStateInvalid                     // attempting to create a rendezvous connection
	sockStateConnecting                  // attempting to create a connection
	sockStateConnected                   // connection is established
	sockStateClosed                      // connection has been closed (by either end)
	sockStateRefused                     // connection rejected by remote host
	sockStateCorrupted                   // peer behaved in an improper manner
	sockStateTimeout                     // connection failed due to peer timeout
)

type recvPktEvent struct {
	pkt packet.Packet
	now time.Time
}

type sendMessage struct {
	content []byte
	tim     time.Time     // time message is submitted
	ttl     time.Duration // message dropped if it can't be sent in this timeframe
}

type shutdownMessage struct {
	sockState    sockState
	permitLinger bool
	err          error
	reason       int
}

/*
udtSocket encapsulates a UDT socket between a local and remote address pair, as
defined by the UDT specification.  udtSocket implements the net.Conn interface
so that it can be used anywhere that a stream-oriented network connection
(like TCP) would be used.
*/
type udtSocket struct {
	// this data not changed after the socket is initialized and/or handshaked
	m *multiplexer // the multiplexer that handles this socket
	//raddr       *net.UDPAddr    // the remote address
	created     time.Time       // the time that this socket was created
	Config      *Config         // configuration parameters for this socket
	udtVer      int             // UDT protcol version (normally 4.  Will we be supporting others?)
	isDatagram  bool            // if true then we're sending and receiving datagrams, otherwise we're a streaming socket
	isServer    bool            // if true then we are behaving like a server, otherwise client (or rendezvous). Only useful during handshake
	sockID      uint32          // our sockID
	farSockID   uint32          // the peer's sockID
	initPktSeq  packet.PacketID // initial packet sequence to start the connection with
	connectWait *sync.WaitGroup // released when connection is complete (or failed)

	sockState           sockState   // socket state - used mostly during handshakes
	maxPacketSize       uint32      // the maximum packet size
	maxFlowWinSize      uint        // receiver: maximum unacknowledged packet count
	currPartialRead     []byte      // stream connections: currently reading message (for partial reads). Owned by client caller (Read)
	readDeadline        *time.Timer // if set, then calls to Read() will return "timeout" after this time
	readDeadlinePassed  bool        // if set, then calls to Read() will return "timeout"
	writeDeadline       *time.Timer // if set, then calls to Write() will return "timeout" after this time
	writeDeadlinePassed bool        // if set, then calls to Write() will return "timeout"

	rttProt sync.RWMutex // lock must be held before referencing rtt/rttVar
	rtt     uint         // receiver: estimated roundtrip time. (in microseconds)
	rttVar  uint         // receiver: roundtrip variance. (in microseconds)

	receiveRateProt sync.RWMutex // lock must be held before referencing deliveryRate/bandwidth
	deliveryRate    uint         // delivery rate reported from peer (packets/sec)
	bandwidth       uint         // bandwidth reported from peer (packets/sec)

	// channels
	messageIn       chan []byte          // inbound messages. Sender is goReceiveEvent->ingestData, Receiver is client caller (Read)
	messageOut      chan sendMessage     // outbound messages. Sender is client caller (Write), Receiver is goSendEvent. Closed when socket is closed
	recvEvent       chan recvPktEvent    // receiver: ingest the specified packet. Sender is readPacket, receiver is goReceiveEvent
	sendEvent       chan recvPktEvent    // sender: ingest the specified packet. Sender is readPacket, receiver is goSendEvent
	sendPacket      chan packet.Packet   // packets to send out on the wire (once goManageConnection is running)
	shutdownEvent   chan shutdownMessage // channel signals the connection to be shutdown
	sockClosed      chan struct{}        // closed when socket is closed
	terminateSignal chan struct{}        // termination signal
	closeMutex      sync.Mutex
	isClosed        bool

	// timers
	connTimeout <-chan time.Time // connecting: fires when connection attempt times out
	connRetry   <-chan time.Time // connecting: fires when connection attempt to be retried
	lingerTimer <-chan time.Time // after disconnection, fires once our linger timer runs out

	send *udtSocketSend // reference to sending side of this socket
	recv *udtSocketRecv // reference to receiving side of this socket
	cong *udtSocketCc   // reference to contestion control

	// performance metrics
	//PktSent      uint64        // number of sent data packets, including retransmissions
	//PktRecv      uint64        // number of received packets
	//PktSndLoss   uint          // number of lost packets (sender side)
	//PktRcvLoss   uint          // number of lost packets (receiver side)
	//PktRetrans   uint          // number of retransmitted packets
	//PktSentACK   uint          // number of sent ACK packets
	//PktRecvACK   uint          // number of received ACK packets
	//PktSentNAK   uint          // number of sent NAK packets
	//PktRecvNAK   uint          // number of received NAK packets
	//MbpsSendRate float64       // sending rate in Mb/s
	//MbpsRecvRate float64       // receiving rate in Mb/s
	//SndDuration  time.Duration // busy sending time (i.e., idle time exclusive)

	// instant measurements
	//PktSndPeriod        time.Duration // packet sending period
	//PktFlowWindow       uint          // flow window size, in number of packets
	//PktCongestionWindow uint          // congestion window size, in number of packets
	//PktFlightSize       uint          // number of packets on flight
	//MsRTT               time.Duration // RTT
	//MbpsBandwidth       float64       // estimated bandwidth, in Mb/s
	//ByteAvailSndBuf     uint          // available UDT sender buffer size
	//ByteAvailRcvBuf     uint          // available UDT receiver buffer size
}

/*******************************************************************************
 Implementation of net.Conn interface
*******************************************************************************/

// Grab the next data packet
func (s *udtSocket) fetchReadPacket(blocking bool) ([]byte, error) {
	var result []byte
	if blocking {
		for {
			if s.readDeadlinePassed {
				return nil, syscall.ETIMEDOUT
			}
			var deadline <-chan time.Time
			if s.readDeadline != nil {
				deadline = s.readDeadline.C
			}
			select {
			case result = <-s.messageIn:
				if result == nil { // nil result indicates EOF
					return nil, io.EOF
				}
				return result, nil
			case _, ok := <-deadline:
				if !ok {
					continue
				}
				s.readDeadlinePassed = true
				return nil, syscall.ETIMEDOUT
			}
		}
	}

	select {
	case result = <-s.messageIn:
		// ok we have a message
	default:
		// ok we've read some stuff and there's nothing immediately available
		return nil, nil
	}
	if result == nil { // nil result indicates EOF. Using this instead of socket state allows to drain any buffered data first.
		return nil, io.EOF
	}
	return result, nil
}

func (s *udtSocket) connectionError() error {
	switch s.sockState {
	case sockStateRefused:
		return errors.New("Connection refused by remote host")
	case sockStateCorrupted:
		return errors.New("Connection closed due to protocol error")
	case sockStateClosed:
		return errors.New("Connection closed")
	case sockStateTimeout:
		return errors.New("Connection timed out")
	}
	return nil
}

// TODO: int sendmsg(const char* data, int len, int msttl, bool inorder)

// Read reads data from the connection.
// Read can be made to time out and return an Error with Timeout() == true
// after a fixed time limit; see SetDeadline and SetReadDeadline.
// (required for net.Conn implementation)
func (s *udtSocket) Read(p []byte) (n int, err error) {
	connErr := s.connectionError()
	if s.isDatagram {
		// for datagram sockets, block until we have a message to return and then return it
		// if the buffer isn't big enough, return a truncated message (discarding the rest) and return an error
		msg, rerr := s.fetchReadPacket(connErr == nil)
		if rerr != nil {
			err = rerr
			return
		}
		if msg == nil && connErr != nil {
			err = connErr
			return
		}
		n = copy(p, msg)
		if n < len(msg) {
			err = errors.New("Message truncated") // <- evil buggy
		}
	} else {
		// for streaming sockets, block until we have at least something to return, then fill up the passed buffer as far as we can without blocking again
		for offset := 0; offset < len(p); {
			if len(s.currPartialRead) == 0 {
				// Grab the next data packet
				if s.currPartialRead, err = s.fetchReadPacket(n == 0 && connErr == nil); err != nil {
					return n, err
				}
				if len(s.currPartialRead) == 0 {
					if n != 0 {
						return
					}
					if connErr != nil {
						return n, connErr
					}
				}
			}

			thisN := copy(p[offset:], s.currPartialRead)

			n += thisN
			offset += thisN
			s.currPartialRead = s.currPartialRead[thisN:]
		}
	}
	return
}

// Write writes data to the connection.
// Write can be made to time out and return an Error with Timeout() == true
// after a fixed time limit; see SetDeadline and SetWriteDeadline.
// (required for net.Conn implementation)
func (s *udtSocket) Write(p []byte) (n int, err error) {
	// at the moment whatever we have right now we'll shove it into a channel and return
	// on the other side:
	//  for datagram sockets: this is a distinct message to be broken into as few packets as possible
	//  for streaming sockets: collect as much as can fit into a packet and send them out
	switch s.sockState {
	case sockStateRefused:
		err = errors.New("Connection refused by remote host")
		return
	case sockStateCorrupted:
		err = errors.New("Connection closed due to protocol error")
		return
	case sockStateClosed:
		err = errors.New("Connection closed")
		return
	}

	// previous bug: io.Writer documentation says "Implementations must not retain p.", but it was passed on in s.messageOut
	n = len(p)
	data := make([]byte, n)
	copy(data, p)

	for {
		if s.writeDeadlinePassed {
			err = syscall.ETIMEDOUT
			return
		}
		var deadline <-chan time.Time
		if s.writeDeadline != nil {
			deadline = s.writeDeadline.C
		}
		select {
		case <-s.terminateSignal:
			return n, errors.New("terminate signal")
		case s.messageOut <- sendMessage{content: data, tim: time.Now()}:
			// send successful
			return
		case _, ok := <-deadline:
			if !ok {
				continue
			}
			s.writeDeadlinePassed = true
			err = syscall.ETIMEDOUT
			return
		}
	}
}

// Close closes the connection.
// Any blocked Read or Write operations will be unblocked.
// Write operations will be permitted to send (initial packets)
// Read operations will return an error // (required for net.Conn implementation).
// Note: Do not simultaneously call Close() and Write(). To close while the socket is still in use, use Terminate().
func (s *udtSocket) Close() error {
	s.closeMutex.Lock()
	defer s.closeMutex.Unlock()

	if s.isClosed || !s.isOpen() {
		return nil // already closed
	}

	s.isClosed = true

	// closing messageOut was a signal supposed to tell the send code to initiate shutdown. However, it closes too fast before all data is transferred.
	// The entire UDT code is a piece of !@#$ and needs a rewrite.
	//close(s.messageOut)
	return nil
}

// Terminate terminates the connection immediately. Unlike Close, it does not permit any reading/writing.
// If the connection should be ordinarily closed (after reading/writing) use Close().
func (s *udtSocket) Terminate() error {
	s.closeMutex.Lock()
	defer s.closeMutex.Unlock()

	if s.isClosed || !s.isOpen() {
		return nil // already closed
	}

	s.isClosed = true

	close(s.terminateSignal)
	return nil
}

func (s *udtSocket) isOpen() bool {
	switch s.sockState {
	case sockStateClosed, sockStateRefused, sockStateCorrupted, sockStateTimeout:
		return false
	default:
		return true
	}
}

// LocalAddr returns the local network address.
// (required for net.Conn implementation)
func (s *udtSocket) LocalAddr() net.Addr {
	//return s.m.laddr
	return nil
}

// RemoteAddr returns the remote network address.
// (required for net.Conn implementation)
func (s *udtSocket) RemoteAddr() net.Addr {
	//return s.raddr
	return nil
}

// SetDeadline sets the read and write deadlines associated
// with the connection. It is equivalent to calling both
// SetReadDeadline and SetWriteDeadline.
//
// A deadline is an absolute time after which I/O operations
// fail with a timeout (see type Error) instead of
// blocking. The deadline applies to all future and pending
// I/O, not just the immediately following call to Read or
// Write. After a deadline has been exceeded, the connection
// can be refreshed by setting a deadline in the future.
//
// An idle timeout can be implemented by repeatedly extending
// the deadline after successful Read or Write calls.
//
// A zero value for t means I/O operations will not time out.
//
// Note that if a TCP connection has keep-alive turned on,
// which is the default unless overridden by Dialer.KeepAlive
// or ListenConfig.KeepAlive, then a keep-alive failure may
// also return a timeout error. On Unix systems a keep-alive
// failure on I/O can be detected using
// errors.Is(err, syscall.ETIMEDOUT).
// (required for net.Conn implementation)
func (s *udtSocket) SetDeadline(t time.Time) error {
	s.setDeadline(t, &s.readDeadline, &s.readDeadlinePassed)
	s.setDeadline(t, &s.writeDeadline, &s.writeDeadlinePassed)
	return nil
}

func (s *udtSocket) setDeadline(dl time.Time, timer **time.Timer, timerPassed *bool) {
	if *timer == nil {
		if !dl.IsZero() {
			*timer = time.NewTimer(dl.Sub(time.Now()))
		}
	} else {
		now := time.Now()
		if !dl.IsZero() && dl.Before(now) {
			*timerPassed = true
		}
		oldTime := *timer
		if dl.IsZero() {
			*timer = nil
		}
		oldTime.Stop()
		_, _ = <-oldTime.C
		if !dl.IsZero() && dl.After(now) {
			*timerPassed = false
			oldTime.Reset(dl.Sub(time.Now()))
		}
	}
}

// SetReadDeadline sets the deadline for future Read calls
// and any currently-blocked Read call.
// A zero value for t means Read will not time out.
// (required for net.Conn implementation)
func (s *udtSocket) SetReadDeadline(t time.Time) error {
	s.setDeadline(t, &s.readDeadline, &s.readDeadlinePassed)
	return nil
}

// SetWriteDeadline sets the deadline for future Write calls
// and any currently-blocked Write call.
// Even if write times out, it may return n > 0, indicating that
// some of the data was successfully written.
// A zero value for t means Write will not time out.
// (required for net.Conn implementation)
func (s *udtSocket) SetWriteDeadline(t time.Time) error {
	s.setDeadline(t, &s.writeDeadline, &s.writeDeadlinePassed)
	return nil
}

/*******************************************************************************
 Private functions
*******************************************************************************/

// newSocket creates a new UDT socket, which will be configured afterwards as either an incoming our outgoing socket
func newSocket(m *multiplexer, config *Config, sockID uint32, isServer bool, isDatagram bool) (s *udtSocket) {
	now := time.Now()

	maxFlowWinSize := config.MaxFlowWinSize
	if maxFlowWinSize == 0 {
		maxFlowWinSize = DefaultConfig().MaxFlowWinSize
	}
	if maxFlowWinSize < 32 {
		maxFlowWinSize = 32
	}

	s = &udtSocket{
		m:      m,
		Config: config,
		//raddr:          raddr,
		created:         now,
		sockState:       sockStateInit,
		udtVer:          4,
		isServer:        isServer,
		maxPacketSize:   uint32(config.MaxPacketSize),
		maxFlowWinSize:  maxFlowWinSize,
		isDatagram:      isDatagram,
		sockID:          sockID,
		initPktSeq:      packet.RandomPacketSequence(),
		messageIn:       make(chan []byte, 256),
		messageOut:      make(chan sendMessage, 256),
		recvEvent:       make(chan recvPktEvent, 256),
		sendEvent:       make(chan recvPktEvent, 256),
		sockClosed:      make(chan struct{}, 1),
		terminateSignal: make(chan struct{}),
		deliveryRate:    16,
		bandwidth:       1,
		sendPacket:      make(chan packet.Packet, 256),
		shutdownEvent:   make(chan shutdownMessage, 5),
	}
	s.cong = newUdtSocketCc(s)

	return
}

func (s *udtSocket) launchProcessors() {
	s.send = newUdtSocketSend(s)
	s.recv = newUdtSocketRecv(s)
	s.cong.init(s.initPktSeq)
}

func (s *udtSocket) startConnect() error {

	connectWait := &sync.WaitGroup{}
	s.connectWait = connectWait
	connectWait.Add(1)

	s.sockState = sockStateConnecting

	s.connTimeout = time.After(3 * time.Second)
	s.connRetry = time.After(250 * time.Millisecond)
	go s.goManageConnection()

	s.sendHandshake(packet.HsRequest)

	connectWait.Wait()
	return s.connectionError()
}

func (s *udtSocket) goManageConnection() {
	for {
		select {
		case <-s.lingerTimer: // linger timer expired, shut everything down
			s.shutdown(sockStateClosed, false, nil, TerminateReasonLingerTimerExpired)
			return
		case <-s.sockClosed:
			return
		case p := <-s.sendPacket:
			ts := uint32(time.Now().Sub(s.created) / time.Microsecond)
			s.cong.onPktSent(p)
			//fmt.Printf("(id=%d) sending %s  (id=%d)\n", s.sockID, packet.PacketTypeName(p.PacketType()), s.farSockID)
			s.m.sendPacket(s.farSockID, ts, p)
		case sd := <-s.shutdownEvent: // connection shut down
			s.shutdown(sd.sockState, sd.permitLinger, sd.err, sd.reason)
		case <-s.connTimeout: // connection timed out
			s.shutdown(sockStateTimeout, true, nil, TerminateReasonConnectTimeout)
		case <-s.connRetry: // resend connection attempt
			s.connRetry = nil
			switch s.sockState {
			case sockStateConnecting:
				s.sendHandshake(packet.HsRequest)
				s.connRetry = time.After(250 * time.Millisecond)
			}
		}
	}
}

func (s *udtSocket) sendHandshake(reqType packet.HandshakeReqType) {
	sockType := packet.TypeSTREAM
	if s.isDatagram {
		sockType = packet.TypeDGRAM
	}

	p := &packet.HandshakePacket{
		UdtVer:     uint32(s.udtVer),
		SockType:   sockType,
		InitPktSeq: s.initPktSeq,
		//MaxPktSize:     s.maxPacketSize,          // maximum packet size (including UDP/IP headers)
		MaxFlowWinSize: uint32(s.maxFlowWinSize), // maximum flow window size
		ReqType:        reqType,
		SockID:         s.sockID,
	}

	ts := uint32(time.Now().Sub(s.created) / time.Microsecond)
	s.cong.onPktSent(p)
	//fmt.Printf("(id=%d) sending handshake(%d) (id=%d)\n", s.sockID, int(reqType), s.farSockID)
	s.m.sendPacket(s.farSockID, ts, p)
}

// checkValidHandshake checks to see if we want to accept a new connection with this handshake.
func (s *udtSocket) checkValidHandshake(m *multiplexer, p *packet.HandshakePacket) bool {
	if s.udtVer != 4 {
		return false
	}
	return true
}

// readHandshake is received when a handshake packet is received without a destination, either as part
// of a listening response or as a rendezvous connection
func (s *udtSocket) readHandshake(m *multiplexer, p *packet.HandshakePacket) bool {
	switch s.sockState {
	case sockStateInit: // server accepting a connection from a client
		s.initPktSeq = p.InitPktSeq
		s.udtVer = int(p.UdtVer)
		s.farSockID = p.SockID
		s.isDatagram = p.SockType == packet.TypeDGRAM

		// MTU negotiation is disabled. Packets may be sent across any network adapter; it would be impossible to use a per-adapter MTU.
		//if s.mtu.get() > p.MaxPktSize {
		//	s.mtu.set(p.MaxPktSize)
		//}
		s.launchProcessors()
		s.recv.configureHandshake(p)
		s.send.configureHandshake(p, true)
		s.sockState = sockStateConnected
		s.connTimeout = nil
		s.connRetry = nil
		go s.goManageConnection()

		s.sendHandshake(packet.HsResponse)
		return true

	case sockStateConnecting: // client attempting to connect to server
		if p.ReqType == packet.HsRefused {
			s.sockState = sockStateRefused
			return true
		}
		if p.ReqType == packet.HsRequest {
			if !s.checkValidHandshake(m, p) || p.InitPktSeq != s.initPktSeq || s.isDatagram != (p.SockType == packet.TypeDGRAM) {
				// ignore, not a valid handshake request
				return true
			}
			// handshake isn't done yet, send it back with the cookie we received
			s.sendHandshake(packet.HsResponse)
			return true
		}
		if p.ReqType != packet.HsResponse {
			// unexpected packet type, ignore
			return true
		}
		if !s.checkValidHandshake(m, p) || p.InitPktSeq != s.initPktSeq || s.isDatagram != (p.SockType == packet.TypeDGRAM) {
			// ignore, not a valid handshake request
			return true
		}
		s.farSockID = p.SockID

		// See documentation above MTU negotation above.
		//if s.mtu.get() > p.MaxPktSize {
		//	s.mtu.set(p.MaxPktSize)
		//}
		s.launchProcessors()
		s.recv.configureHandshake(p)
		s.send.configureHandshake(p, true)
		s.connRetry = nil
		s.sockState = sockStateConnected
		s.connTimeout = nil
		if s.connectWait != nil {
			s.connectWait.Done()
			s.connectWait = nil
		}
		return true

	case sockStateConnected: // server repeating a handshake to a client
		if s.isServer && p.ReqType == packet.HsRequest {
			// client didn't receive our response handshake, resend it
			s.sendHandshake(packet.HsResponse)
		} else if !s.isServer && p.ReqType == packet.HsResponse {
			// this is a rendezvous connection (re)send our response
			s.sendHandshake(packet.HsResponse2)
		}
		return true
	}

	return false
}

func (s *udtSocket) shutdown(sockState sockState, permitLinger bool, err error, reason int) {
	if !s.isOpen() {
		return // already closed
	}
	//if err != nil {
	//	fmt.Printf("socket shutdown (type=%d), due to error: %s\n", int(sockState), err.Error())
	//} else {
	//	fmt.Printf("socket shutdown (type=%d) (permitLinger = %t, duration = %s)\n", int(sockState), permitLinger, s.Config.LingerTime.String())
	//}

	if permitLinger {
		linger := s.Config.LingerTime
		if linger == 0 {
			linger = DefaultConfig().LingerTime
		}
		s.lingerTimer = time.After(linger)
		s.m.closer.CloseLinger(reason)
		return
	}

	if s.connectWait != nil {
		s.connectWait.Done()
		s.connectWait = nil
	}
	s.sockState = sockState
	s.cong.close()

	s.connTimeout = nil
	s.connRetry = nil
	close(s.sockClosed)
	close(s.recvEvent)

	s.m.closer.Close(reason)

	s.messageIn <- nil
}

func absdiff(a uint, b uint) uint {
	if a < b {
		return b - a
	}
	return a - b
}

func (s *udtSocket) applyRTT(rtt uint) {
	s.rttProt.Lock()
	s.rttVar = (s.rttVar*3 + absdiff(s.rtt, rtt)) / 4 //>> 2 //
	s.rtt = (s.rtt*7 + rtt) / 8                       // >> 3
	s.rttProt.Unlock()
}

func (s *udtSocket) getRTT() (rtt, rttVar uint) {
	s.rttProt.RLock()
	rtt = s.rtt
	rttVar = s.rttVar
	s.rttProt.RUnlock()
	return
}

// Update Estimated Bandwidth and packet delivery rate
func (s *udtSocket) applyReceiveRates(deliveryRate uint, bandwidth uint) {
	s.receiveRateProt.Lock()
	if deliveryRate > 0 {
		s.deliveryRate = (s.deliveryRate*7 + deliveryRate) / 8 // >> 3
	}
	if bandwidth > 0 {
		s.bandwidth = (s.bandwidth*7 + bandwidth) / 8 // >> 3
	}
	s.receiveRateProt.Unlock()
}

func (s *udtSocket) getRcvSpeeds() (deliveryRate uint, bandwidth uint) {
	s.receiveRateProt.RLock()
	deliveryRate = s.deliveryRate
	bandwidth = s.bandwidth
	s.receiveRateProt.RUnlock()
	return
}

// called by the multiplexer read loop when a packet is received for this socket.
// Minimal processing is permitted but try not to stall the caller
func (s *udtSocket) readPacket(m *multiplexer, p packet.Packet) {
	now := time.Now()
	if s.sockState == sockStateClosed {
		return
	}

	s.recvEvent <- recvPktEvent{pkt: p, now: now}

	switch sp := p.(type) {
	case *packet.HandshakePacket: // sent by both peers
		s.readHandshake(m, sp)
	case *packet.ShutdownPacket: // sent by either peer
		s.shutdownEvent <- shutdownMessage{sockState: sockStateClosed, permitLinger: s.isServer, reason: TerminateReasonRemoteSentShutdown} // if client tells us done, it is done.
	case *packet.AckPacket, *packet.NakPacket: // receiver -> sender
		s.sendEvent <- recvPktEvent{pkt: p, now: now}
	case *packet.UserDefControlPacket:
		s.cong.onCustomMsg(*sp)
	}
}
