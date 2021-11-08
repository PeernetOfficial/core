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
	messageOut    <-chan sendMessage     // outbound messages. Sender is client caller (Write), Receiver is goSendEvent. Closed when socket is closed
	sendPacket    chan<- packet.Packet   // send a packet out on the wire
	shutdownEvent chan<- shutdownMessage // channel signals the connection to be shutdown
	socket        *udtSocket

	sendState      sendState        // current sender state
	sendPktPend    *sendPacketHeap  // list of packets that have been sent but not yet acknowledged
	sendPktSeq     packet.PacketID  // the current packet sequence number
	msgPartialSend *sendMessage     // when a message can only partially fit in a socket, this is the remainder
	msgSeq         uint32           // the current message sequence number
	lastRecvTime   time.Time        // the last time we've heard something from the remote system
	recvAckSeq     packet.PacketID  // largest packetID we've received an ACK from
	sendLossList   *receiveLossHeap // loss list
	sndPeriod      atomicDuration   // (set by congestion control) delay between sending packets
	congestWindow  atomicUint32     // (set by congestion control) size of the current congestion window (in packets)
	flowWindowSize uint             // negotiated maximum number of unacknowledged packets (in packets)

	// timers
	sndEvent <-chan time.Time // if a packet is recently sent, this timer fires when SND completes
}

func newUdtSocketSend(s *udtSocket) *udtSocketSend {
	ss := &udtSocketSend{
		socket:         s,
		sendPktSeq:     s.initPktSeq,
		sockClosed:     s.sockClosed,
		sendEvent:      s.sendEvent,
		messageOut:     s.messageOut,
		congestWindow:  atomicUint32{val: 16},
		flowWindowSize: s.maxFlowWinSize,
		sendPacket:     s.sendPacket,
		shutdownEvent:  s.shutdownEvent,
		sendPktPend:    createPacketHeap(),
		sendLossList:   createPacketIDHeap(),
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
				s.shutdownEvent <- shutdownMessage{sockState: sockStateClosed, permitLinger: !s.socket.isServer, reason: TerminateReasonCannotProcessOutgoing}
				return
			}
			s.msgPartialSend = &msg
			s.processDataMsg(true, messageOut)
		case evt, ok := <-sendEvent:
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
			s.sendState = s.reevalSendState()
		case _, _ = <-sockClosed:
			return
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
	cwnd := uint(s.congestWindow.get())
	if cwnd > s.flowWindowSize {
		cwnd = s.flowWindowSize
	}
	if uint(s.sendPktPend.Count()) > cwnd {
		return sendStateWaiting
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
	if s.sendLossList.Count() == 0 || s.sendPktPend.Count() == 0 {
		return false
	}

	for _, entry := range s.sendLossList.Range(s.recvAckSeq.Seq, s.sendPktSeq.Seq) {
		dp := s.sendPktPend.Find(entry.packetID.Seq)
		if dp == nil {
			// can't find record of this packet, not much we can do really
			continue
		}

		if dp.ttl != 0 && time.Now().Add(dp.ttl).After(dp.tim) {
			// this packet has expired, ignore
			continue
		}

		s.sendDataPacket(*dp, true)
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

// we have a packed packet and a green light to send, so lets send this and mark it
func (s *udtSocketSend) sendDataPacket(dp sendPacketEntry, isResend bool) {
	// packets that are being resent are not stored on the 'to be acknowledged' list.
	// It would not make any sense and introduce race condition with potential endless packet resends/ACKs.
	// Once the remote peer ACKs a sent packet, it is removed from the list.
	if !isResend {
		s.sendPktPend.Add(dp)
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

	if !s.assertValidSentPktID("ACK", p.PktSeqHi, TerminateReasonInvalidPacketIDAck) || p.PktSeqHi.BlindDiff(s.recvAckSeq) <= 0 {
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
}

// ingestNak is called to process an NAK packet
func (s *udtSocketSend) ingestNak(p *packet.NakPacket, now time.Time) {
	var lossList []packet.PacketID
	clen := len(p.CmpLossInfo)
	for idx := 0; idx < clen; idx++ {
		thisEntry := p.CmpLossInfo[idx]
		if thisEntry&0x80000000 != 0 {
			thisPktID := packet.PacketID{Seq: thisEntry & 0x7FFFFFFF}
			if idx+1 == clen {
				s.shutdownEvent <- shutdownMessage{sockState: sockStateCorrupted, permitLinger: false,
					err: fmt.Errorf("FAULT: While unpacking a NAK, the last entry (%x) was describing a start-of-range", thisEntry), reason: TerminateReasonCorruptPacketNak}
				return
			}
			if !s.assertValidSentPktID("NAK", thisPktID, TerminateReasonInvalidPacketIDNak) {
				return
			}
			lastEntry := p.CmpLossInfo[idx+1]
			if lastEntry&0x80000000 != 0 {
				s.shutdownEvent <- shutdownMessage{sockState: sockStateCorrupted, permitLinger: false,
					err: fmt.Errorf("FAULT: While unpacking a NAK, a start-of-range (%x) was followed by another start-of-range (%x)", thisEntry, lastEntry), reason: TerminateReasonCorruptPacketNak}
				return
			}
			lastPktID := packet.PacketID{Seq: lastEntry}
			if !s.assertValidSentPktID("NAK", lastPktID, TerminateReasonInvalidPacketIDNak) {
				return
			}
			idx++
			for span := thisPktID; span != lastPktID; span.Incr() {
				s.sendLossList.Add(recvLossEntry{packetID: packet.PacketID{Seq: span.Seq}})
				lossList = append(lossList, packet.PacketID{Seq: span.Seq})
			}
		} else {
			thisPktID := packet.PacketID{Seq: thisEntry}
			if !s.assertValidSentPktID("NAK", thisPktID, TerminateReasonInvalidPacketIDNak) {
				return
			}
			s.sendLossList.Add(recvLossEntry{packetID: thisPktID})
			lossList = append(lossList, thisPktID)
		}
	}

	s.socket.cong.onNAK(lossList)

	s.sendState = sendStateProcessDrop // immediately restart transmission
}

// ingestCongestion is called to process a (retired?) Congestion packet
func (s *udtSocketSend) ingestCongestion(p *packet.CongestionPacket, now time.Time) {
	// One way packet delay is increasing, so decrease the sending rate
	// this is very rough (not atomic, doesn't inform congestion) but this is a deprecated message in any case
	s.sndPeriod.set(s.sndPeriod.get() * 1125 / 1000)
	//m_iLastDecSeq = s.sendPktSeq
}
