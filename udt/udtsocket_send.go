package udt

import (
	"container/heap"
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
	messageOut    <-chan sendMessage     // outbound messages. Sender is client caller (Write), Receiver is goSendEvent. Closed when socket is closed
	sendPacket    chan<- packet.Packet   // send a packet out on the wire
	shutdownEvent chan<- shutdownMessage // channel signals the connection to be shutdown
	socket        *udtSocket

	sendState      sendState       // current sender state
	sendPktPend    sendPacketHeap  // list of packets that have been sent but not yet acknowledged
	sendPktSeq     packet.PacketID // the current packet sequence number
	msgPartialSend *sendMessage    // when a message can only partially fit in a socket, this is the remainder
	msgSeq         uint32          // the current message sequence number
	expCount       uint            // number of continuous EXP timeouts.
	lastRecvTime   time.Time       // the last time we've heard something from the remote system
	recvAckSeq     packet.PacketID // largest packetID we've received an ACK from
	sendLossList   packetIDHeap    // loss list
	sndPeriod      atomicDuration  // (set by congestion control) delay between sending packets
	rtoPeriod      atomicDuration  // (set by congestion control) override of EXP timer calculations
	congestWindow  atomicUint32    // (set by congestion control) size of the current congestion window (in packets)
	flowWindowSize uint            // negotiated maximum number of unacknowledged packets (in packets)

	// timers
	sndEvent      <-chan time.Time // if a packet is recently sent, this timer fires when SND completes
	expTimerEvent <-chan time.Time // Fires when we haven't heard from the peer in a while
}

func newUdtSocketSend(s *udtSocket) *udtSocketSend {
	ss := &udtSocketSend{
		socket:         s,
		expCount:       1,
		sendPktSeq:     s.initPktSeq,
		sockClosed:     s.sockClosed,
		sendEvent:      s.sendEvent,
		messageOut:     s.messageOut,
		congestWindow:  atomicUint32{val: 16},
		flowWindowSize: s.maxFlowWinSize,
		sendPacket:     s.sendPacket,
		shutdownEvent:  s.shutdownEvent,
	}
	ss.resetEXP(s.created)
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

func (s *udtSocketSend) goSendEvent() {
	sendEvent := s.sendEvent
	messageOut := s.messageOut
	sockClosed := s.sockClosed
	for {
		thisMsgChan := messageOut

		switch s.sendState {
		case sendStateIdle: // not waiting for anything, can send immediately
			if s.msgPartialSend != nil { // we have a partial message waiting, try to send more of it now
				s.processDataMsg(false, messageOut)
				continue
			}
		case sendStateProcessDrop: // immediately re-process any drop list requests
			s.sendState = s.reevalSendState() // try to reconstruct what our state should be if it wasn't sendStateProcessDrop
			if !s.processSendLoss() || s.sendPktSeq.Seq%16 == 0 {
				s.processSendExpire()
			}
			continue
		default:
			thisMsgChan = nil
		}

		select {
		case msg, ok := <-thisMsgChan: // nil if we can't process outgoing messages right now
			if !ok {
				s.sendPacket <- &packet.ShutdownPacket{}
				s.shutdownEvent <- shutdownMessage{sockState: sockStateClosed, permitLinger: !s.socket.isServer}
				return
			}
			s.msgPartialSend = &msg
			s.processDataMsg(true, messageOut)
		case evt, ok := <-sendEvent:
			if !ok {
				return
			}
			s.expCount = 1
			s.resetEXP(evt.now)
			switch sp := evt.pkt.(type) {
			case *packet.AckPacket:
				s.ingestAck(sp, evt.now)
			case *packet.NakPacket:
				s.ingestNak(sp, evt.now)
			case *packet.CongestionPacket:
				s.ingestCongestion(sp, evt.now)
			}
			s.sendState = s.reevalSendState()
		case _, _ = <-sockClosed:
			return
		case now := <-s.expTimerEvent: // EXP event
			s.expEvent(now)
		case <-s.sndEvent: // SND event
			s.sndEvent = nil
			if s.sendState == sendStateSending {
				s.sendState = s.reevalSendState()
				if !s.processSendLoss() || s.sendPktSeq.Seq%16 == 0 {
					s.processSendExpire()
				}
			}
		}
	}
}

func (s *udtSocketSend) reevalSendState() sendState {
	if s.sndEvent != nil {
		return sendStateSending
	}
	// Do we have too many unacknowledged packets for us to send any more?
	if s.sendPktPend != nil {
		congestWindow := uint(s.congestWindow.get())
		cwnd := s.flowWindowSize
		if cwnd > congestWindow {
			cwnd = congestWindow
		}
		if uint(len(s.sendPktPend)) >= cwnd {
			return sendStateWaiting
		}
	}
	return sendStateIdle
}

// try to pack a new data packet and send it
func (s *udtSocketSend) processDataMsg(isFirst bool, inChan <-chan sendMessage) {
	for s.msgPartialSend != nil {
		partialSend := s.msgPartialSend
		state := packet.MbOnly
		if s.socket.isDatagram {
			if isFirst {
				state = packet.MbFirst
			} else {
				state = packet.MbMiddle
			}
		}
		if isFirst || !s.socket.isDatagram {
			s.msgSeq++
		}

		mtu := int(s.socket.maxPacketSize) - 16
		msgLen := len(partialSend.content)

		dp := &packet.DataPacket{
			Seq: s.sendPktSeq,
		}

		if msgLen >= mtu {
			// we are full -- send what we can and leave the rest
			dp.Data = partialSend.content[0:mtu]
			if msgLen == mtu {
				s.msgPartialSend = nil
			} else {
				s.msgPartialSend = &sendMessage{content: partialSend.content[mtu:], tim: partialSend.tim, ttl: partialSend.ttl}
			}
		} else {
			// we are not full -- send only if this is a datagram or there's nothing obvious left
			if s.socket.isDatagram {
				// datagram
				if isFirst {
					state = packet.MbOnly
				} else {
					state = packet.MbLast
				}
			} else {
				// streaming socket
				select {
				case morePartialSend, ok := <-inChan:
					if ok {
						// we have more data, concat and try again
						s.msgPartialSend = &sendMessage{
							content: append(s.msgPartialSend.content, morePartialSend.content...),
							tim:     s.msgPartialSend.tim,
							ttl:     s.msgPartialSend.ttl,
						}
						continue
					}
				default:
					// nothing immediately available, just send what we have
				}
			}

			partialSend = s.msgPartialSend
			dp.Data = partialSend.content
			s.msgPartialSend = nil
		}

		s.sendPktSeq.Incr()
		dp.SetMessageData(state, !s.socket.isDatagram, s.msgSeq)
		s.sendDataPacket(sendPacketEntry{pkt: dp, tim: partialSend.tim, ttl: partialSend.ttl}, false)

		// Return makes sense here so that the sending loop can stop in case the remote peer misses packets and reports a nak.
		return
	}
}

// If the sender's loss list is not empty, retransmit the first packet in the list and remove it from the list.
func (s *udtSocketSend) processSendLoss() bool {
	if s.sendLossList == nil || s.sendPktPend == nil {
		return false
	}

	var dp *sendPacketEntry
	for {
		minLoss, minLossIdx := s.sendLossList.Min(s.recvAckSeq, s.sendPktSeq)
		if minLossIdx < 0 {
			// empty loss list? shouldn't really happen as we don't keep empty lists, but check for it anyhow
			return false
		}

		heap.Remove(&s.sendLossList, minLossIdx)
		if len(s.sendLossList) == 0 {
			s.sendLossList = nil
		}

		dp, _ = s.sendPktPend.Find(minLoss)
		if dp == nil {
			// can't find record of this packet, not much we can do really
			continue
		}

		if dp.ttl != 0 && time.Now().Add(dp.ttl).After(dp.tim) {
			// this packet has expired, ignore
			continue
		}

		break
	}

	s.sendDataPacket(*dp, true)
	return true
}

// evaluate our pending packet list to see if we have any expired messages
func (s *udtSocketSend) processSendExpire() bool {
	if s.sendPktPend == nil {
		return false
	}

	pktPend := make([]sendPacketEntry, len(s.sendPktPend))
	copy(pktPend, s.sendPktPend)
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
				if s.sendLossList != nil {
					if _, slIdx := s.sendLossList.Find(p.pkt.Seq); slIdx >= 0 {
						heap.Remove(&s.sendLossList, slIdx)
					}
				}
			}
			if s.sendLossList != nil && len(s.sendLossList) == 0 {
				s.sendLossList = nil
			}

			s.sendPacket <- dropMsg
			return true
		}
	}
	return false
}

// we have a packed packet and a green light to send, so lets send this and mark it
func (s *udtSocketSend) sendDataPacket(dp sendPacketEntry, isResend bool) {
	// packets that are being resent are not stored on the 'to be acknowledged' list.
	// It would not make any sense and introduce race condition with potential endless packet resends/ACKs.
	// Once the remote peer ACKs a sent packet, it is removed from the list.
	if !isResend {
		if s.sendPktPend == nil {
			s.sendPktPend = sendPacketHeap{dp}
			heap.Init(&s.sendPktPend)
		} else {
			heap.Push(&s.sendPktPend, dp)
		}
	}

	s.socket.cong.onDataPktSent(dp.pkt.Seq)
	s.sendPacket <- dp.pkt

	// have we exceeded our recipient's window size?
	s.sendState = s.reevalSendState()
	if s.sendState == sendStateWaiting {
		return
	}

	if !isResend && dp.pkt.Seq.Seq%16 == 0 {
		s.processSendExpire()
		return
	}

	snd := s.sndPeriod.get()
	if snd > 0 {
		s.sndEvent = time.After(snd)
		s.sendState = sendStateSending
	}
}

func (s *udtSocketSend) assertValidSentPktID(pktType string, pktSeq packet.PacketID) bool {
	if s.sendPktSeq.BlindDiff(pktSeq) < 0 {
		s.shutdownEvent <- shutdownMessage{sockState: sockStateCorrupted, permitLinger: false,
			err: fmt.Errorf("FAULT: Received an %s for packet %d, but the largest packet we've sent has been %d", pktType, pktSeq.Seq, s.sendPktSeq.Seq)}
		return false
	}
	return true
}

// ingestAck is called to process an ACK packet
func (s *udtSocketSend) ingestAck(p *packet.AckPacket, now time.Time) {
	// Update the largest acknowledged sequence number.

	// Send back an ACK2 with the same ACK sequence number in this ACK.
	s.sendPacket <- &packet.Ack2Packet{AckSeqNo: p.AckSeqNo}

	if !s.assertValidSentPktID("ACK", p.PktSeqHi) || p.PktSeqHi.BlindDiff(s.recvAckSeq) <= 0 {
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
	if s.sendPktPend != nil {
		for {
			minLoss, minLossIdx := s.sendPktPend.Min(oldAckSeq, s.sendPktSeq)
			if p.PktSeqHi.BlindDiff(minLoss.Seq) >= 0 || minLossIdx < 0 {
				break
			}
			heap.Remove(&s.sendPktPend, minLossIdx)
		}
		if len(s.sendPktPend) == 0 {
			s.sendPktPend = nil
		}
	}

	// Update sender's loss list (by removing all those that has been acknowledged).
	if s.sendLossList != nil {
		for {
			minLoss, minLossIdx := s.sendLossList.Min(oldAckSeq, s.sendPktSeq)
			if p.PktSeqHi.BlindDiff(minLoss) >= 0 || minLossIdx < 0 {
				break
			}
			heap.Remove(&s.sendLossList, minLossIdx)
		}
		if len(s.sendLossList) == 0 {
			s.sendLossList = nil
		}
	}
}

// ingestNak is called to process an NAK packet
func (s *udtSocketSend) ingestNak(p *packet.NakPacket, now time.Time) {
	newLossList := make([]packet.PacketID, 0)
	clen := len(p.CmpLossInfo)
	for idx := 0; idx < clen; idx++ {
		thisEntry := p.CmpLossInfo[idx]
		if thisEntry&0x80000000 != 0 {
			thisPktID := packet.PacketID{Seq: thisEntry & 0x7FFFFFFF}
			if idx+1 == clen {
				s.shutdownEvent <- shutdownMessage{sockState: sockStateCorrupted, permitLinger: false,
					err: fmt.Errorf("FAULT: While unpacking a NAK, the last entry (%x) was describing a start-of-range", thisEntry)}
				return
			}
			if !s.assertValidSentPktID("NAK", thisPktID) {
				return
			}
			lastEntry := p.CmpLossInfo[idx+1]
			if lastEntry&0x80000000 != 0 {
				s.shutdownEvent <- shutdownMessage{sockState: sockStateCorrupted, permitLinger: false,
					err: fmt.Errorf("FAULT: While unpacking a NAK, a start-of-range (%x) was followed by another start-of-range (%x)", thisEntry, lastEntry)}
				return
			}
			lastPktID := packet.PacketID{Seq: lastEntry}
			if !s.assertValidSentPktID("NAK", lastPktID) {
				return
			}
			idx++
			for span := thisPktID; span != lastPktID; span.Incr() {
				newLossList = append(newLossList, span)
			}
		} else {
			thisPktID := packet.PacketID{Seq: thisEntry}
			if !s.assertValidSentPktID("NAK", thisPktID) {
				return
			}
			newLossList = append(newLossList, thisPktID)
		}
	}

	s.socket.cong.onNAK(newLossList)

	if s.sendLossList == nil {
		s.sendLossList = newLossList
		heap.Init(&s.sendLossList)
	} else {
		llen := len(newLossList)
		for idx := 0; idx < llen; idx++ {
			heap.Push(&s.sendLossList, newLossList[idx])
		}
	}

	s.sendState = sendStateProcessDrop // immediately restart transmission
}

// ingestCongestion is called to process a (retired?) Congestion packet
func (s *udtSocketSend) ingestCongestion(p *packet.CongestionPacket, now time.Time) {
	// One way packet delay is increasing, so decrease the sending rate
	// this is very rough (not atomic, doesn't inform congestion) but this is a deprecated message in any case
	s.sndPeriod.set(s.sndPeriod.get() * 1125 / 1000)
	//m_iLastDecSeq = s.sendPktSeq
}

func (s *udtSocketSend) resetEXP(now time.Time) {
	s.lastRecvTime = now

	var nextExpDurn time.Duration
	rtoPeriod := s.rtoPeriod.get()
	if rtoPeriod > 0 {
		nextExpDurn = rtoPeriod
	} else {
		rtt, rttVar := s.socket.getRTT()
		nextExpDurn = (time.Duration(s.expCount*(rtt+4*rttVar))*time.Microsecond + s.socket.Config.SynTime)
		minExpTime := time.Duration(s.expCount) * minEXPinterval
		if nextExpDurn < minExpTime {
			nextExpDurn = minExpTime
		}
	}
	s.expTimerEvent = time.After(nextExpDurn)
}

// we've just had the EXP timer expire, see what we can do to recover this
func (s *udtSocketSend) expEvent(currTime time.Time) {

	// Haven't receive any information from the peer, is it dead?!
	// timeout: at least 16 expirations and must be greater than 10 seconds
	if (s.expCount > 16) && (currTime.Sub(s.lastRecvTime) > 5*time.Second) {
		// Connection is broken.
		s.shutdownEvent <- shutdownMessage{sockState: sockStateTimeout, permitLinger: true}
		return
	}

	// sender: Insert all the packets sent after last received acknowledgement into the sender loss list.
	// recver: Send out a keep-alive packet
	if s.sendPktPend != nil {
		if s.sendPktPend != nil && s.sendLossList == nil {
			// resend all unacknowledged packets on timeout, but only if there is no packet in the loss list
			newLossList := make([]packet.PacketID, 0)
			for span := s.recvAckSeq.Add(1); span != s.sendPktSeq.Add(1); span.Incr() {
				newLossList = append(newLossList, span)
			}
			s.sendLossList = newLossList
			heap.Init(&s.sendLossList)
		}
		s.socket.cong.onTimeout()
		s.sendState = sendStateProcessDrop // immediately restart transmission
	} else {
		s.sendPacket <- &packet.KeepAlivePacket{}
	}

	s.expCount++
	// Reset last response time since we just sent a heart-beat.
	s.resetEXP(currTime)
}
