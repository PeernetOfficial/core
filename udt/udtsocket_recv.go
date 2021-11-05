package udt

import (
	"time"

	"github.com/PeernetOfficial/core/udt/packet"
)

type udtSocketRecv struct {
	// channels
	sockClosed <-chan struct{}      // closed when socket is closed
	recvEvent  <-chan recvPktEvent  // receiver: ingest the specified packet. Sender is readPacket, receiver is goReceiveEvent
	messageIn  chan<- []byte        // inbound messages. Sender is goReceiveEvent->ingestData, Receiver is client caller (Read)
	sendPacket chan<- packet.Packet // send a packet out on the wire
	socket     *udtSocket

	farNextPktSeq      packet.PacketID  // the peer's next largest packet ID expected.
	farRecdPktSeq      packet.PacketID  // the peer's last "received" packet ID (before any loss events)
	lastACK            uint32           // last ACK packet we've sent
	largestACK         uint32           // largest ACK packet we've sent that has been acknowledged (by an ACK2).
	recvPktPend        *sendPacketHeap  // list of packets that are waiting to be processed.
	recvLossList       *receiveLossHeap // loss list.
	ackHistory         *ackHistoryHeap  // list of sent ACKs.
	sentAck            packet.PacketID  // largest packetID we've sent an ACK regarding
	recvAck2           packet.PacketID  // largest packetID we've received an ACK2 from
	recvLastArrival    time.Time        // time of the most recent data packet arrival
	recvLastProbe      time.Time        // time of the most recent data packet probe packet
	ackPeriod          atomicDuration   // (set by congestion control) delay between sending ACKs
	unackPktCount      uint             // number of packets we've received that we haven't sent an ACK for
	recvPktHistory     []time.Duration  // list of recently received packets.
	recvPktPairHistory []time.Duration  // probing packet window.

	// timers
	ackSentEvent2 <-chan time.Time // if an ACK packet has recently sent, don't include link information in the next one
	ackSentEvent  <-chan time.Time // if an ACK packet has recently sent, wait before resending it
}

func newUdtSocketRecv(s *udtSocket) *udtSocketRecv {
	sr := &udtSocketRecv{
		socket:       s,
		sockClosed:   s.sockClosed,
		recvEvent:    s.recvEvent,
		messageIn:    s.messageIn,
		sendPacket:   s.sendPacket,
		recvPktPend:  createPacketHeap(),
		recvLossList: createPacketIDHeap(),
		ackHistory:   createHistoryHeap(),
	}
	go sr.goReceiveEvent()
	return sr
}

func (s *udtSocketRecv) configureHandshake(p *packet.HandshakePacket) {
	s.farNextPktSeq = p.InitPktSeq
	s.farRecdPktSeq = p.InitPktSeq.Add(-1)
	s.sentAck = p.InitPktSeq
	s.recvAck2 = p.InitPktSeq
}

func (s *udtSocketRecv) goReceiveEvent() {
	recvEvent := s.recvEvent
	sockClosed := s.sockClosed
	for {
		select {
		case evt, ok := <-recvEvent:
			if !ok {
				return
			}
			switch sp := evt.pkt.(type) {
			case *packet.Ack2Packet:
				s.ingestAck2(sp, evt.now)
			case *packet.MsgDropReqPacket:
				s.ingestMsgDropReq(sp, evt.now)
			case *packet.DataPacket:
				s.ingestData(sp, evt.now)
			case *packet.ErrPacket:
				s.ingestError(sp)
			}
		case _, _ = <-sockClosed: // socket is closed, leave now
			return
		case <-s.ackSentEvent:
			s.ackSentEvent = nil
		case <-s.ackSentEvent2:
			s.ackSentEvent2 = nil
		}
	}
}

/*
ACK is used to trigger an acknowledgement (ACK). Its period is set by
   the congestion control module. However, UDT will send an ACK no
   longer than every 0.01 second, even though the congestion control
   does not need timer-based ACK. Here, 0.01 second is defined as the
   SYN time, or synchronization time, and it affects many of the other
   timers used in UDT.

   NAK is used to trigger a negative acknowledgement (NAK). Its period
   is dynamically updated to 4 * RTT_+ RTTVar + SYN, where RTTVar is the
   variance of RTT samples.

   EXP is used to trigger data packets retransmission and maintain
   connection status. Its period is dynamically updated to N * (4 * RTT
   + RTTVar + SYN), where N is the number of continuous timeouts. To
   avoid unnecessary timeout, a minimum threshold (e.g., 0.5 second)
   should be used in the implementation.
*/

// ingestAck2 is called to process an ACK2 packet
func (s *udtSocketRecv) ingestAck2(p *packet.Ack2Packet, now time.Time) {
	ackHistEntry := s.ackHistory.Remove(p.AckSeqNo)
	if ackHistEntry == nil {
		return // this ACK not found
	}
	if s.recvAck2.BlindDiff(ackHistEntry.lastPacket) < 0 {
		s.recvAck2 = ackHistEntry.lastPacket
	}

	// Update the largest ACK number ever been acknowledged.
	if s.largestACK < p.AckSeqNo {
		s.largestACK = p.AckSeqNo
	}

	s.socket.applyRTT(uint(now.Sub(ackHistEntry.sendTime) / time.Microsecond))

	//s.rto = 4 * s.rtt + s.rttVar
}

// ingestMsgDropReq is called to process an message drop request packet
func (s *udtSocketRecv) ingestMsgDropReq(p *packet.MsgDropReqPacket, now time.Time) {
	stopSeq := p.LastSeq.Add(1)
	for pktID := p.FirstSeq; pktID != stopSeq; pktID.Incr() {
		// remove all these packets from the loss list
		s.recvLossList.Remove(pktID.Seq)

		// remove all pending packets with this message
		s.recvPktPend.Remove(pktID.Seq)
	}

	if p.FirstSeq == s.farRecdPktSeq.Add(1) {
		s.farRecdPktSeq = p.LastSeq
	}
	// if s.recvLossList.Count() == 0 {
	// 	s.farRecdPktSeq = s.farNextPktSeq.Add(-1)
	// }

	// try to push any pending packets out, now that we have dropped any blocking packets
	for _, nextPkt := range s.recvPktPend.Range(stopSeq.Seq, s.farNextPktSeq.Seq) {
		if !s.attemptProcessPacket(nextPkt.pkt, false) {
			break
		}
	}
}

// ingestData is called to process a data packet
func (s *udtSocketRecv) ingestData(p *packet.DataPacket, now time.Time) {
	s.socket.cong.onPktRecv(*p)

	seq := p.Seq

	/* If the sequence number of the current data packet is 16n + 1,
	where n is an integer, record the time interval between this
	packet and the last data packet in the Packet Pair Window. */
	if (seq.Seq-1)&0xf == 0 {
		if !s.recvLastProbe.IsZero() {
			if s.recvPktPairHistory == nil {
				s.recvPktPairHistory = []time.Duration{now.Sub(s.recvLastProbe)}
			} else {
				s.recvPktPairHistory = append(s.recvPktPairHistory, now.Sub(s.recvLastProbe))
				if len(s.recvPktPairHistory) > 16 {
					s.recvPktPairHistory = s.recvPktPairHistory[len(s.recvPktPairHistory)-16:]
				}
			}
		}
		s.recvLastProbe = now
	}

	// Record the packet arrival time in PKT History Window.
	if !s.recvLastArrival.IsZero() {
		if s.recvPktHistory == nil {
			s.recvPktHistory = []time.Duration{now.Sub(s.recvLastArrival)}
		} else {
			s.recvPktHistory = append(s.recvPktHistory, now.Sub(s.recvLastArrival))
			if len(s.recvPktHistory) > 16 {
				s.recvPktHistory = s.recvPktHistory[len(s.recvPktHistory)-16:]
			}
		}
	}
	s.recvLastArrival = now

	/* If the sequence number of the current data packet is greater
	than LRSN + 1, put all the sequence numbers between (but
	excluding) these two values into the receiver's loss list and
	send them to the sender in an NAK packet. */
	seqDiff := seq.BlindDiff(s.farNextPktSeq)
	if seqDiff > 0 {
		for n := uint32(0); n < uint32(seqDiff); n++ {
			s.recvLossList.Add(recvLossEntry{packetID: packet.PacketID{Seq: (s.farNextPktSeq.Seq + n) & 0x7FFFFFFF}})
		}

		s.sendNAK(s.farNextPktSeq.Seq, uint32(seqDiff))
		s.farNextPktSeq = seq.Add(1)

	} else if seqDiff < 0 {
		// If the sequence number is less than LRSN, remove it from the receiver's loss list.
		if !s.recvLossList.Remove(seq.Seq) {
			return // already previously received packet -- ignore
		}

		if s.recvLossList.Count() == 0 {
			s.farRecdPktSeq = s.farNextPktSeq.Add(-1)
		} else {
			if minR := s.recvLossList.Min(s.farRecdPktSeq.Seq, s.farNextPktSeq.Seq); minR != nil {
				s.farRecdPktSeq = packet.PacketID{Seq: minR.Seq}
			}
		}
	} else {
		s.farNextPktSeq = seq.Add(1)
	}

	s.attemptProcessPacket(p, true)
}

func (s *udtSocketRecv) attemptProcessPacket(p *packet.DataPacket, isNew bool) bool {
	seq := p.Seq

	// can we process this packet?
	boundary, mustOrder, msgID := p.GetMessageData()
	if s.recvLossList.Count() > 0 && mustOrder && s.farRecdPktSeq.Add(1) != seq {
		// we're required to order these packets and we're missing prior packets, so push and return
		if isNew {
			s.recvPktPend.Add(sendPacketEntry{pkt: p})
		}
		return false
	}

	// can we find the start of this message?
	pieces := make([]*packet.DataPacket, 0)
	cannotContinue := false
	switch boundary {
	case packet.MbLast, packet.MbMiddle:
		// we need prior packets, let's make sure we have them
		if s.recvPktPend.Count() > 0 {
			pieceSeq := seq.Add(-1)
			for {
				prevPiece := s.recvPktPend.Find(pieceSeq.Seq)
				if prevPiece == nil {
					// we don't have the previous piece, is it missing?
					if s.recvLossList.Count() > 0 {
						if s.recvLossList.Find(pieceSeq.Seq) != nil {
							// it's missing, stop processing
							cannotContinue = true
						}
					}
					// in any case we can't continue with this
					break
				}
				prevBoundary, _, prevMsg := prevPiece.pkt.GetMessageData()
				if prevMsg != msgID {
					// ...oops? previous piece isn't in the same message
					break
				}
				pieces = append([]*packet.DataPacket{prevPiece.pkt}, pieces...)
				if prevBoundary == packet.MbFirst {
					break
				}
				pieceSeq.Decr()
			}
		}
	}
	if !cannotContinue {
		pieces = append(pieces, p)

		switch boundary {
		case packet.MbFirst, packet.MbMiddle:
			// we need following packets, let's make sure we have them
			if s.recvPktPend.Count() > 0 {
				pieceSeq := seq.Add(1)
				for {
					nextPiece := s.recvPktPend.Find(pieceSeq.Seq)
					if nextPiece == nil {
						// we don't have the previous piece, is it missing?
						if pieceSeq == s.farNextPktSeq {
							// hasn't been received yet
							cannotContinue = true
						} else if s.recvLossList.Count() > 0 {
							if s.recvLossList.Find(pieceSeq.Seq) != nil {
								// it's missing, stop processing
								cannotContinue = true
							}
						} else {
						}
						// in any case we can't continue with this
						break
					}
					nextBoundary, _, nextMsg := nextPiece.pkt.GetMessageData()
					if nextMsg != msgID {
						// ...oops? previous piece isn't in the same message
						break
					}
					pieces = append(pieces, nextPiece.pkt)
					if nextBoundary == packet.MbLast {
						break
					}
				}
			}
		}
	}

	// Acknowledge the packet if the threshold is reached. This used to be a parameter s.ackInterval supposed to be set by congestion control, but never was.
	// Before, there was both the (unused) ACK interval s.ackInterval and s.ackTimerEvent which fired at SynTime, which was way too often and basically a ddos.
	// It makes more sense to just send the ACK x split of the congestion window.
	s.unackPktCount++
	// DEBUG: Always send ack for now
	//if s.unackPktCount >= s.socket.cong.GetCongestionWindowSize()/4 {
	s.ackEvent()
	//}

	if cannotContinue {
		// we need to wait for more packets, store and return
		if isNew {
			s.recvPktPend.Add(sendPacketEntry{pkt: p})
		}
		return false
	}

	// we have a message, pull it from the pending heap (if necessary), assemble it into a message, and return it
	if s.recvPktPend.Count() > 0 {
		for _, piece := range pieces {
			s.recvPktPend.Remove(piece.Seq.Seq)
		}
	}

	msg := make([]byte, 0)
	for _, piece := range pieces {
		msg = append(msg, piece.Data...)
	}
	s.messageIn <- msg
	return true
}

func (s *udtSocketRecv) getRcvSpeeds() (recvSpeed, bandwidth int) {

	// get median value, but cannot change the original value order in the window
	if s.recvPktHistory != nil {
		ourPktHistory := make(sortableDurnArray, len(s.recvPktHistory))
		copy(ourPktHistory, s.recvPktHistory)
		n := len(ourPktHistory)

		cutPos := n / 2
		FloydRivestBuckets(ourPktHistory, cutPos)
		median := ourPktHistory[cutPos]

		upper := median << 3  // upper bounds
		lower := median >> 3  // lower bounds
		count := 0            // number of entries inside bounds
		var sum time.Duration // sum of values inside bounds

		// median filtering
		idx := 0
		for i := 0; i < n; i++ {
			if (ourPktHistory[idx] < upper) && (ourPktHistory[idx] > lower) {
				count++
				sum += ourPktHistory[idx]
			}
			idx++
		}

		// do we have enough valid values to return a value?
		// calculate speed
		if count > (n >> 1) {
			recvSpeed = int(time.Second * time.Duration(count) / sum)
		}
	}

	// get median value, but cannot change the original value order in the window
	if s.recvPktPairHistory != nil {
		ourProbeHistory := make(sortableDurnArray, len(s.recvPktPairHistory))
		copy(ourProbeHistory, s.recvPktPairHistory)
		n := len(ourProbeHistory)

		cutPos := n / 2
		FloydRivestBuckets(ourProbeHistory, cutPos)
		median := ourProbeHistory[cutPos]

		upper := median << 3 // upper bounds
		lower := median >> 3 // lower bounds
		count := 1           // number of entries inside bounds
		sum := median        // sum of values inside bounds

		// median filtering
		idx := 0
		for i := 0; i < n; i++ {
			if (ourProbeHistory[idx] < upper) && (ourProbeHistory[idx] > lower) {
				count++
				sum += ourProbeHistory[idx]
			}
			idx++
		}

		bandwidth = int(time.Second * time.Duration(count) / sum)
	}

	return
}

func (s *udtSocketRecv) sendACK() {
	var ack packet.PacketID

	// If there is no loss, the ACK is the current largest sequence number plus 1;
	// Otherwise it is the smallest sequence number in the receiver loss list.
	if s.recvLossList.Count() == 0 {
		ack = s.farNextPktSeq
	} else {
		ack = s.farRecdPktSeq.Add(1)
	}

	if ack == s.recvAck2 {
		return
	}

	// only send out an ACK if we either are saying something new or the ackSentEvent has expired
	if ack == s.sentAck && s.ackSentEvent != nil {
		return
	}
	s.sentAck = ack

	s.lastACK++
	s.ackHistory.Add(ackHistoryEntry{
		ackID:      s.lastACK,
		lastPacket: ack,
		sendTime:   time.Now(),
	})

	rtt, rttVar := s.socket.getRTT()

	numPendPackets := int(s.farNextPktSeq.BlindDiff(s.farRecdPktSeq) - 1)
	availWindow := int(s.socket.maxFlowWinSize) - numPendPackets
	if availWindow < 2 {
		availWindow = 2
	}

	p := &packet.AckPacket{
		AckSeqNo:  s.lastACK,
		PktSeqHi:  ack,
		Rtt:       uint32(rtt),
		RttVar:    uint32(rttVar),
		BuffAvail: uint32(availWindow),
	}
	if s.ackSentEvent2 == nil {
		recvSpeed, bandwidth := s.getRcvSpeeds()
		p.IncludeLink = true
		p.PktRecvRate = uint32(recvSpeed)
		p.EstLinkCap = uint32(bandwidth)
		s.ackSentEvent2 = time.After(s.socket.Config.SynTime)
	}
	s.sendPacket <- p
	s.ackSentEvent = time.After(time.Duration(rtt+4*rttVar) * time.Microsecond)
}

func (s *udtSocketRecv) sendNAK(sequenceFrom uint32, count uint32) {
	lossInfo := make([]uint32, 0)
	for n := uint32(0); n < count; n++ {
		lossInfo = append(lossInfo, (sequenceFrom+n)&0x7FFFFFFF)
	}

	s.sendPacket <- &packet.NakPacket{CmpLossInfo: lossInfo}
}

// ingestData is called to process an (undocumented) OOB error packet
func (s *udtSocketRecv) ingestError(p *packet.ErrPacket) {
	// TODO: umm something
}

// assuming some condition has occured (ACK timer expired, ACK interval), send an ACK and reset the appropriate timer
func (s *udtSocketRecv) ackEvent() {
	s.sendACK()
	s.unackPktCount = 0
}
