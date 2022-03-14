package udt

import (
	"fmt"
	"time"

	"github.com/PeernetOfficial/core/udt/packet"
)

type sendState int

const (
	sendStateIdle        sendState = iota // not waiting for anything, can send immediately
	sendStateSending                      // recently sent something, waiting for SND before sending more
	sendStateWaiting                      // destination is full, waiting for them to process something and come back
	sendStateProcessDrop                  // immediately re-process any drop list requests
)

const (
	minEXPinterval time.Duration = 300 * time.Millisecond
)

type udtSocketSend struct {
	// channels
	sockClosed    <-chan struct{}        // closed when socket is closed
	sendEvent     <-chan recvPktEvent    // sender: ingest the specified packet. Sender is readPacket, receiver is goSendEvent
	messageOut    <-chan sendMessage     // outbound data messages. Sender is client caller (Write), Receiver is goSendEvent. Closed when socket is closed
	sendPacket    chan<- packet.Packet   // send a packet out on the wire
	shutdownEvent chan<- shutdownMessage // channel signals the connection to be shutdown
	socket        *UDTSocket

	sendState       sendState        // current sender state
	sendPktPend     *sendPacketHeap  // list of packets that have been sent but not yet acknowledged
	sendPktSeq      packet.PacketID  // the current packet sequence number
	msgRemainder    *sendMessage     // when a message can only partially fit in a socket, this is the remainder
	msgSeq          uint32           // the current message sequence number
	lastSendTime    time.Time        // the last time we've sent a data packet to the remote system
	recvAckSeq      packet.PacketID  // largest packetID we've received an ACK from
	sendLossList    *receiveLossHeap // loss list. New entries added via incoming NAK.
	sndPeriod       atomicDuration   // (set by congestion control) delay between sending packets
	congestWindow   atomicUint32     // (set by congestion control) size of the current congestion window (in packets)
	flowWindowSize  uint             // negotiated maximum number of unacknowledged packets (in packets)
	resendDataTimer <-chan time.Time // Timer for resending outgoing data packets
	resendDataTime  time.Duration    // Doubles after every send to prevent ddos
}

func newUdtSocketSend(s *UDTSocket) *udtSocketSend {
	ss := &udtSocketSend{
		socket:          s,
		sendPktSeq:      s.initPktSeq,
		sockClosed:      s.sockClosed,
		sendEvent:       s.sendEvent,
		messageOut:      s.messageOut,
		congestWindow:   atomicUint32{val: 16},
		flowWindowSize:  s.maxFlowWinSize,
		sendPacket:      s.sendPacket,
		shutdownEvent:   s.shutdownEvent,
		sendPktPend:     createPacketHeap(),
		sendLossList:    createPacketIDHeap(),
		resendDataTimer: make(chan time.Time),
	}
	go ss.goSendEvent()
	return ss
}

func (s *udtSocketSend) configureHandshake(p *packet.HandshakePacket, resetSeq bool) {
	if resetSeq {
		s.recvAckSeq = p.InitPktSeq
		s.sendPktSeq = p.InitPktSeq
	}
	s.flowWindowSize = uint(p.MaxFlowWinSize)
}

func (s *udtSocketSend) SetPacketSendPeriod(snd time.Duration) {
	// check to see if we have a bandwidth limit here
	maxBandwidth := s.socket.Config.MaxBandwidth
	if maxBandwidth > 0 {
		minSP := time.Second / time.Duration(float64(maxBandwidth)/float64(s.socket.maxPacketSize))
		if snd < minSP {
			snd = minSP
		}
	}

	s.sndPeriod.set(snd)
}

// goSendData loops to send data
func (s *udtSocketSend) goSendEvent() {
	// isSendPeriodExpired returns a channel that will be signaled when a new packet can be sent.
	isSendPeriodExpired := func() (eventTimer <-chan time.Time) {
		if s.lastSendTime.IsZero() {
			return nil
		}

		sendPeriod := s.sndPeriod.get()
		if sendPeriod == 0 {
			return nil
		}

		diff := time.Since(s.lastSendTime)
		if diff > sendPeriod {
			return nil
		}

		// not waited long enough, return a timer
		return time.After(diff - sendPeriod)
	}

	for {
		// immediately send out remainder?
		if s.sendState == sendStateSending {
			s.processDataMsg(s.msgRemainder.content, s.msgRemainder.tim, s.msgRemainder.ttl, false)
			s.reevalSendState()
		}

		// use some channels only depending on the current sending state
		var messageOut <-chan sendMessage
		var eventTimer <-chan time.Time

		switch s.sendState {
		case sendStateIdle:
			// Wait for new messages from upstream to send out. No congestion reported downstream.
			if eventTimer = isSendPeriodExpired(); eventTimer == nil {
				messageOut = s.messageOut
			}

		case sendStateSending:
			if eventTimer = isSendPeriodExpired(); eventTimer == nil {
				// Note: It probably makes sense to check here s.sendEvent if there is immediately a message, to not delay processing of NAKs.
				continue
			}

		case sendStateWaiting:
			// Destination is full (congested). Do not use event timer, do not check for new messages. Only wait for incoming ACKs + resend data packets.

		case sendStateProcessDrop:
			// Immediately resend any missing packets. The status will only be updated by incoming ACKs.
			if !s.processSendLoss() || s.sendPktSeq.Seq%16 == 0 {
				s.processSendExpire()
			}
		}

		// wait for a channel to fire
		select {
		case msg, ok := <-messageOut: // nil if we can't process outgoing messages right now, which means it will not be selected
			// new message outgoing
			if !ok {
				s.sendPacket <- &packet.ShutdownPacket{}
				s.shutdownEvent <- shutdownMessage{sockState: sockStateClosed, permitLinger: !s.socket.isServer, reason: TerminateReasonSocketClosed}
				return
			}

			msg.content = s.fillDataToMTU(msg.content, messageOut) // a trick to fill up the packet immediately with data (stream only)

			s.processDataMsg(msg.content, msg.tim, msg.ttl, true)

			s.reevalSendState() // check if congested and update as appropriate

		case <-eventTimer:

		case evt, ok := <-s.sendEvent:
			if !ok {
				return
			}
			switch sp := evt.pkt.(type) {
			case *packet.AckPacket:
				s.ingestAck(sp, evt.now)
			case *packet.NakPacket:
				s.ingestNak(sp, evt.now)
			case *packet.CongestionPacket:
				s.ingestCongestion(sp, evt.now)
			}

		case <-s.sockClosed:
			return

		case <-s.socket.terminateSignal:
			s.sendPacket <- &packet.ShutdownPacket{}
			s.shutdownEvent <- shutdownMessage{sockState: sockStateClosed, permitLinger: false, reason: TerminateReasonSignal}
			return

		case <-s.resendDataTimer:
			// Resend data that was not acknowledged yet.
			for _, dp := range s.sendPktPend.list {
				s.sendPacket <- dp.pkt
			}

			// to prevent ddos, always double the time
			s.resendDataTime = s.resendDataTime * 2
			s.resendDataTimer = time.NewTimer(s.resendDataTime).C
		}
	}
}

// reevalSendState updates the send state to idle/send/wait as appropriate.
func (s *udtSocketSend) reevalSendState() sendState {
	// Do we have too many unacknowledged packets for us to send any more?
	cwnd := uint(s.congestWindow.get())
	if cwnd > s.flowWindowSize {
		cwnd = s.flowWindowSize
	}
	if uint(s.sendPktPend.Count()) > cwnd {
		s.sendState = sendStateWaiting

		// set the timer for constantly resending data packets until ACKed
		s.resendDataTime = s.socket.Config.SynTime
		s.resendDataTimer = time.NewTimer(s.resendDataTime).C

		return s.sendState
	}

	if s.sendState == sendStateWaiting {
		// constant resending no longer needed
		s.resendDataTimer = make(chan time.Time)
	}

	// is the current packet data to send empty? Switch to idle in this case.
	if s.msgRemainder == nil {
		s.sendState = sendStateIdle
	} else {
		s.sendState = sendStateSending
	}

	return s.sendState
}

// fillDataToMTU tries to fill up data until MTU is reached if data is immediately available in the channel. Only for streaming socket.
func (s *udtSocketSend) fillDataToMTU(data []byte, dataChan <-chan sendMessage) (dataFilled []byte) {
	if s.socket.isDatagram {
		return data
	}
	mtu := int(s.socket.maxPacketSize) - 16 // 16 = data packet header

	// Continue until the data reaches the max packet length
	for len(data) < mtu {
		select {
		case morePartialSend := <-dataChan:
			if len(morePartialSend.content) == 0 { // Indicates EOF.
				return data
			}

			// we have more data, concat and try again
			data = append(data, morePartialSend.content...)
			continue
		default:
			// nothing immediately available, just send what we have
			return data
		}
	}
	return data
}

// try to pack a new data packet and send it
// The remainder will be stored to s.msgRemainder (otherwise it will be cleared). It is the callers responsibility to continue sending as appropriate (and use isFirst).
func (s *udtSocketSend) processDataMsg(data []byte, tim time.Time, ttl time.Duration, isFirst bool) {
	mtu := int(s.socket.maxPacketSize) - 16 // 16 = data packet header

	// determine the MessageBoundary
	state := packet.MbOnly // for stream
	if s.socket.isDatagram {
		switch {
		case isFirst && len(data) > mtu:
			state = packet.MbFirst
		case isFirst && len(data) <= mtu:
			state = packet.MbOnly
		case !isFirst && len(data) > mtu:
			state = packet.MbMiddle
		case !isFirst && len(data) <= mtu:
			state = packet.MbLast
		}
	}

	// partial send?
	if len(data) > mtu {
		s.msgRemainder = &sendMessage{content: data[mtu:], tim: tim, ttl: ttl}
		data = data[:mtu]
	} else {
		s.msgRemainder = nil
	}

	s.sendDataPacket(data, state, tim, ttl)
}

// sendDataPacket sends a new data packet immediately. Do not use this function for resendig an already sent packet.
func (s *udtSocketSend) sendDataPacket(data []byte, state packet.MessageBoundary, tim time.Time, ttl time.Duration) {
	// set the sequence number
	dp := &packet.DataPacket{
		Seq:  s.sendPktSeq,
		Data: data,
	}
	s.sendPktSeq.Incr()

	// set the message control bits (top three bits)
	dp.SetMessageData(state, !s.socket.isDatagram, s.msgSeq)

	// Datagram messages: Increase message counter if first, otherwise for stream each one is a new message.
	if state == packet.MbFirst || !s.socket.isDatagram {
		s.msgSeq++
	}

	// Add packet to the 'to be acknowledged' list.
	// Once the remote peer ACKs a sent packet, it is removed from the list.
	s.sendPktPend.Add(sendPacketEntry{pkt: dp, tim: tim, ttl: ttl})

	// send on the wire
	s.socket.cong.onDataPktSent(dp.Seq)
	s.sendPacket <- dp

	s.lastSendTime = time.Now()
}

// If the sender's loss list is not empty, retransmit the first packet in the list and remove it from the list.
func (s *udtSocketSend) processSendLoss() bool {
	if s.sendLossList.Count() == 0 || s.sendPktPend.Count() == 0 {
		return false
	}

	activeLossList := s.sendLossList.Range(s.recvAckSeq, s.sendPktSeq)
	if len(activeLossList) == 0 { // edge case which should never happen, but clean it up in case
		s.sendLossList.list = []recvLossEntry{}
		return false
	}

	for _, entry := range activeLossList {
		// Make sure each missing record is only resent every X time to prevent endless ddos. Waiting time for resend doubles each send.
		if !entry.lastResend.IsZero() && entry.lastResend.Add(s.socket.Config.SynTime*time.Duration(entry.attemptsResend)).After(time.Now()) {
			continue
		}
		entry.lastResend = time.Now()
		entry.attemptsResend++

		dp, found := s.sendPktPend.Find(entry.packetID.Seq)
		if !found {
			// can't find record of this packet, not much we can do really. Remove it from the list.
			// in the future perhaps send the info that this message was dropped?
			s.sendLossList.Remove(entry.packetID.Seq)
			continue
		}

		if dp.ttl != 0 && time.Now().Add(dp.ttl).After(dp.tim) {
			// this packet has expired, ignore
			continue
		}

		// resend the packet
		s.socket.cong.onDataPktSent(dp.pkt.Seq)
		s.sendPacket <- dp.pkt
	}

	return true
}

// evaluate our pending packet list to see if we have any expired messages
func (s *udtSocketSend) processSendExpire() bool {
	if s.sendPktPend.Count() == 0 {
		return false
	}

	pktPend := make([]sendPacketEntry, s.sendPktPend.Count())
	copy(pktPend, s.sendPktPend.list)
	for _, p := range pktPend {
		if p.ttl != 0 && time.Now().Add(p.ttl).After(p.tim) {
			// this message has expired, drop it
			_, _, msgNo := p.pkt.GetMessageData()
			dropMsg := &packet.MsgDropReqPacket{
				MsgID:    msgNo,
				FirstSeq: p.pkt.Seq,
				LastSeq:  p.pkt.Seq,
			}

			// find the other packets in this message
			for _, op := range pktPend {
				_, _, otherMsgNo := op.pkt.GetMessageData()
				if otherMsgNo == msgNo {
					if dropMsg.FirstSeq.BlindDiff(p.pkt.Seq) > 0 {
						dropMsg.FirstSeq = p.pkt.Seq
					}
					if dropMsg.LastSeq.BlindDiff(p.pkt.Seq) < 0 {
						dropMsg.LastSeq = p.pkt.Seq
					}
				}
				s.sendLossList.Remove(p.pkt.Seq.Seq)
			}

			s.sendPacket <- dropMsg
			return true
		}
	}
	return false
}

func (s *udtSocketSend) assertValidSentPktID(pktType string, pktSeq packet.PacketID, reason int) bool {
	if s.sendPktSeq.BlindDiff(pktSeq) < 0 {
		s.shutdownEvent <- shutdownMessage{sockState: sockStateCorrupted, permitLinger: false,
			err: fmt.Errorf("FAULT: Received an %s for packet %d, but the largest packet we've sent has been %d", pktType, pktSeq.Seq, s.sendPktSeq.Seq), reason: reason}
		return false
	}
	return true
}

// ingestAck is called to process an ACK packet
func (s *udtSocketSend) ingestAck(p *packet.AckPacket, now time.Time) {
	// Update the largest acknowledged sequence number.

	// Send back an ACK2 with the same ACK sequence number in this ACK.
	s.sendPacket <- &packet.Ack2Packet{AckSeqNo: p.AckSeqNo}

	if !s.assertValidSentPktID("ACK", p.PktSeqHi, TerminateReasonInvalidPacketIDAck) || p.PktSeqHi.IsLessEqual(s.recvAckSeq) {
		return
	}

	oldAckSeq := s.recvAckSeq
	s.flowWindowSize = uint(p.BuffAvail)
	s.recvAckSeq = p.PktSeqHi

	// Update RTT and RTTVar.
	s.socket.applyRTT(uint(p.Rtt))

	// Update flow window size.
	if p.IncludeLink {
		s.socket.applyReceiveRates(uint(p.PktRecvRate), uint(p.EstLinkCap))
	}

	s.socket.cong.onACK(p.PktSeqHi)

	// Update packet arrival rate: A = (A * 7 + a) / 8, where a is the value carried in the ACK.
	// Update estimated link capacity: B = (B * 7 + b) / 8, where b is the value carried in the ACK.

	// Update sender's list of packets that have been sent but not yet acknowledged
	s.sendPktPend.RemoveRange(oldAckSeq, p.PktSeqHi)

	// Update sender's loss list (by removing all those that has been acknowledged).
	s.sendLossList.RemoveRange(oldAckSeq, p.PktSeqHi)

	// Unlock for sending as appropriate
	s.reevalSendState()
}

// ingestNak is called to process an NAK packet
func (s *udtSocketSend) ingestNak(p *packet.NakPacket, now time.Time) {
	var lossList []packet.PacketID

	for n := 0; n < len(p.CmpLossInfo); n++ {
		lossID := p.CmpLossInfo[n]

		// Ignore loss IDs smaller than previous ACK (note that s.recvAckSeq is excluding).
		// It is a possible race condition that the receiver receives packets out of order, sends a NAK and immediately an ACK (which may arrive in different order).
		if (packet.PacketID{Seq: lossID}).IsLess(s.recvAckSeq) {
			continue
		}

		if lossID&0x80000000 != 0 {
			thisPktID := packet.PacketID{Seq: lossID & 0x7FFFFFFF}
			if n+1 == len(p.CmpLossInfo) {
				s.shutdownEvent <- shutdownMessage{sockState: sockStateCorrupted, permitLinger: false,
					err: fmt.Errorf("FAULT: While unpacking a NAK, the last entry (%x) was describing a start-of-range", lossID), reason: TerminateReasonCorruptPacketNak}
				return
			}
			if !s.assertValidSentPktID("NAK", thisPktID, TerminateReasonInvalidPacketIDNak) {
				return
			}
			lastEntry := p.CmpLossInfo[n+1]
			if lastEntry&0x80000000 != 0 {
				s.shutdownEvent <- shutdownMessage{sockState: sockStateCorrupted, permitLinger: false,
					err: fmt.Errorf("FAULT: While unpacking a NAK, a start-of-range (%x) was followed by another start-of-range (%x)", lossID, lastEntry), reason: TerminateReasonCorruptPacketNak}
				return
			}
			lastPktID := packet.PacketID{Seq: lastEntry}
			if !s.assertValidSentPktID("NAK", lastPktID, TerminateReasonInvalidPacketIDNak) {
				return
			}
			n++
			for span := thisPktID; span != lastPktID; span.Incr() {
				s.sendLossList.Add(recvLossEntry{packetID: packet.PacketID{Seq: span.Seq}})
				lossList = append(lossList, packet.PacketID{Seq: span.Seq})
			}
		} else {
			thisPktID := packet.PacketID{Seq: lossID}
			if !s.assertValidSentPktID("NAK", thisPktID, TerminateReasonInvalidPacketIDNak) {
				return
			}
			s.sendLossList.Add(recvLossEntry{packetID: thisPktID})
			lossList = append(lossList, thisPktID)
		}
	}

	s.socket.cong.onNAK(lossList)

	// Some loss entries may be discarded if out of date (already ACK received), so make sure loss list contains entries before changing the sending state.
	if s.sendLossList.Count() > 0 {
		s.sendState = sendStateProcessDrop // immediately restart transmission

		// resending now orderly handled via NAKs instead of constant data packet resending
		s.resendDataTimer = make(chan time.Time)
	}
}

// ingestCongestion is called to process a (retired?) Congestion packet
func (s *udtSocketSend) ingestCongestion(p *packet.CongestionPacket, now time.Time) {
	// One way packet delay is increasing, so decrease the sending rate
	// this is very rough (not atomic, doesn't inform congestion) but this is a deprecated message in any case
	s.sndPeriod.set(s.sndPeriod.get() * 1125 / 1000)
	//m_iLastDecSeq = s.sendPktSeq
}
