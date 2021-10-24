package udt

import (
	"time"

	"github.com/PeernetOfficial/core/udt/packet"
)

type congMsgType int

const (
	congInit congMsgType = iota
	congClose
	congOnACK
	congOnNAK
	congOnTimeout
	congOnDataPktSent
	congOnPktSent
	congOnPktRecv
	congOnCustomMsg
)

type congMsg struct {
	mtyp  congMsgType
	pktID packet.PacketID
	arg   interface{}
}

type udtSocketCc struct {
	// channels
	sockClosed <-chan struct{} // closed when socket is closed
	socket     *udtSocket
	congestion CongestionControl // congestion control object for this socket
	msgs       chan congMsg

	sendPktSeq packet.PacketID // packetID of most recently sent packet
	congWindow uint            // size of congestion window (in packets)
	sndPeriod  time.Duration   // delay between sending packets
}

func newUdtSocketCc(s *udtSocket) *udtSocketCc {
	newCongestion := s.Config.CongestionForSocket
	if newCongestion == nil {
		newCongestion = DefaultConfig().CongestionForSocket
	}

	sc := &udtSocketCc{
		socket:     s,
		sockClosed: s.sockClosed,
		congestion: newCongestion(s),
		msgs:       make(chan congMsg, 100),
	}
	go sc.goCongestionEvent()
	return sc
}

func (s *udtSocketCc) goCongestionEvent() {
	msgs := s.msgs
	sockClosed := s.sockClosed
	for {
		select {
		case evt, ok := <-msgs:
			if !ok {
				return
			}
			switch evt.mtyp {
			case congInit:
				s.sendPktSeq = evt.pktID
				s.congestion.Init(s, s.socket.Config.SynTime)
			case congClose:
				s.congestion.Close(s)
			case congOnACK:
				s.congestion.OnACK(s, evt.pktID)
			case congOnNAK:
				s.congestion.OnNAK(s, evt.arg.([]packet.PacketID))
			case congOnTimeout:
				s.congestion.OnTimeout(s)
			case congOnDataPktSent:
				s.sendPktSeq = evt.pktID
			case congOnPktSent:
				s.congestion.OnPktSent(s, evt.arg.(packet.Packet))
			case congOnPktRecv:
				s.congestion.OnPktRecv(s, evt.arg.(packet.DataPacket))
			case congOnCustomMsg:
				s.congestion.OnCustomMsg(s, evt.arg.(packet.UserDefControlPacket))
			}
		case _, _ = <-sockClosed:
			return
		}
	}
}

// Init to be called (only) at the start of a UDT connection.
func (s *udtSocketCc) init(sendPktSeq packet.PacketID) {
	s.msgs <- congMsg{
		mtyp:  congInit,
		pktID: sendPktSeq,
	}
}

// Close to be called when a UDT connection is closed.
func (s *udtSocketCc) close() {
	s.msgs <- congMsg{
		mtyp: congClose,
	}
}

// OnACK to be called when an ACK packet is received
func (s *udtSocketCc) onACK(pktID packet.PacketID) {
	s.msgs <- congMsg{
		mtyp:  congOnACK,
		pktID: pktID,
	}
}

// OnNAK to be called when a loss report is received
func (s *udtSocketCc) onNAK(loss []packet.PacketID) {
	var ourLoss = make([]packet.PacketID, len(loss))
	copy(ourLoss, loss)

	s.msgs <- congMsg{
		mtyp: congOnNAK,
		arg:  ourLoss,
	}
}

// OnTimeout to be called when a timeout event occurs
func (s *udtSocketCc) onTimeout() {
	s.msgs <- congMsg{
		mtyp: congOnTimeout,
	}
}

// OnPktSent to be called when data is sent
func (s *udtSocketCc) onDataPktSent(pktID packet.PacketID) {
	s.msgs <- congMsg{
		mtyp:  congOnDataPktSent,
		pktID: pktID,
	}
}

// OnPktSent to be called when data is sent
func (s *udtSocketCc) onPktSent(p packet.Packet) {
	s.msgs <- congMsg{
		mtyp: congOnPktSent,
		arg:  p,
	}
}

// OnPktRecv to be called when data is received
func (s *udtSocketCc) onPktRecv(p packet.DataPacket) {
	s.msgs <- congMsg{
		mtyp: congOnPktRecv,
		arg:  p,
	}
}

// OnCustomMsg to process a user-defined packet
func (s *udtSocketCc) onCustomMsg(p packet.UserDefControlPacket) {
	s.msgs <- congMsg{
		mtyp: congOnCustomMsg,
		arg:  p,
	}
}

// GetSndCurrSeqNo is the most recently sent packet ID
func (s *udtSocketCc) GetSndCurrSeqNo() packet.PacketID {
	return s.sendPktSeq
}

// SetCongestionWindowSize sets the size of the congestion window (in packets)
func (s *udtSocketCc) SetCongestionWindowSize(pkt uint) {
	s.congWindow = pkt
	s.socket.send.congestWindow.set(uint32(pkt))
}

// GetCongestionWindowSize gets the size of the congestion window (in packets)
func (s *udtSocketCc) GetCongestionWindowSize() uint {
	return s.congWindow
}

// GetPacketSendPeriod gets the current delay between sending packets
func (s *udtSocketCc) GetPacketSendPeriod() time.Duration {
	return s.sndPeriod
}

// SetPacketSendPeriod sets the current delay between sending packets
func (s *udtSocketCc) SetPacketSendPeriod(snd time.Duration) {
	s.sndPeriod = snd
	s.socket.send.SetPacketSendPeriod(snd)
}

// GetMaxFlowWindow is the largest number of unacknowledged packets we can receive (in packets)
func (s *udtSocketCc) GetMaxFlowWindow() uint {
	return s.socket.maxFlowWinSize
}

// GetReceiveRates is the current calculated receive rate and bandwidth (in packets/sec)
func (s *udtSocketCc) GetReceiveRates() (uint, uint) {
	return s.socket.getRcvSpeeds()
}

// GetRTT is the current calculated roundtrip time between peers
func (s *udtSocketCc) GetRTT() time.Duration {
	rtt, _ := s.socket.getRTT()
	return time.Duration(rtt) * time.Microsecond
}

// GetMSS is the largest packet size we can currently send (in bytes)
func (s *udtSocketCc) GetMSS() uint {
	return uint(s.socket.mtu.get())
}

// SetACKPerid sets the time between ACKs sent to the peer
func (s *udtSocketCc) SetACKPeriod(ack time.Duration) {
	s.socket.recv.ackPeriod.set(ack)
}

// SetACKInterval sets the number of packets sent to the peer before sending an ACK
func (s *udtSocketCc) SetACKInterval(ack uint) {
	s.socket.recv.ackInterval.set(uint32(ack))
}

// SetRTOPeriod overrides the default EXP timeout calculations waiting for data from the peer
func (s *udtSocketCc) SetRTOPeriod(rto time.Duration) {
	s.socket.send.rtoPeriod.set(rto)
}
