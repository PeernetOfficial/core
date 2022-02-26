package udt

import (
	"time"

	"github.com/PeernetOfficial/core/udt/packet"
)

type udtSocketRecv struct {
	// channels
	recvEvent  <-chan recvPktEvent  // receiver: ingest the specified packet. Sender is readPacket, receiver is goReceiveEvent
	messageIn  chan<- []byte        // inbound messages. Sender is goReceiveEvent->ingestData, Receiver is client caller (Read)
	sendPacket chan<- packet.Packet // send a packet out on the wire
	socket     *udtSocket

	nextSequenceExpect packet.PacketID  // the peer's next largest packet ID expected.
	lastSequence       packet.PacketID  // the peer's last received packet ID before any loss events
	lastACKID          uint32           // last ACK packet we've sent
	recvPktPend        *sendPacketHeap  // list of packets that are waiting to be processed.
	recvLossList       *receiveLossHeap // loss list.
	ackHistory         *ackHistoryHeap  // list of sent ACKs.
	sentAck            packet.PacketID  // largest packetID we've sent an ACK regarding
	recvAck2           packet.PacketID  // largest packetID we've received an ACK2 from
	recvLastArrival    time.Time        // time of the most recent data packet arrival
	recvLastProbe      time.Time        // time of the most recent data packet probe packet
	ackPeriod          atomicDuration   // (set by congestion control) delay between sending ACKs. Currently not used.
	ackInterval        atomicUint32     // (set by congestion control) number of data packets to send before sending an ACK
	unackPktCount      uint             // number of packets we've received that we haven't sent an ACK for
	recvPktHistory     []time.Duration  // list of recently received packets.
	recvPktPairHistory []time.Duration  // probing packet window.
	ackLinkInfoSent    time.Time        // when link info was sent in ACK packet last time
	resendACKTimer     <-chan time.Time // Timer for resending outgoing ACK
	resendACKTicker    time.Ticker      // Ticker for resending outgoing ACK
	resendACKLimiter   rateLimiter      // Doubles after every resend to prevent ddos
	resendNAKLimiter   rateLimiter      // Doubles after every resend to prevent ddos
}

func newUdtSocketRecv(s *udtSocket) *udtSocketRecv {
	sr := &udtSocketRecv{
		socket:           s,
		recvEvent:        s.recvEvent,
		messageIn:        s.messageIn,
		sendPacket:       s.sendPacket,
		recvPktPend:      createPacketHeap(),
		recvLossList:     createPacketIDHeap(),
		ackHistory:       createHistoryHeap(),
		resendACKLimiter: rateLimiter{MinWaitTime: s.Config.SynTime, MaxWaitTime: time.Second},
		resendNAKLimiter: rateLimiter{MinWaitTime: s.Config.SynTime, MaxWaitTime: time.Second},
	}

	// set the timer for constantly resending ACKs for the highest sequence ID and NAKs for missing packets
	sr.resendACKTicker = *time.NewTicker(s.Config.SynTime)
	sr.resendACKTimer = sr.resendACKTicker.C

	go sr.goReceiveEvent()

	return sr
}

func (s *udtSocketRecv) configureHandshake(p *packet.HandshakePacket) {
	s.nextSequenceExpect = p.InitPktSeq
	s.lastSequence = p.InitPktSeq.Add(-1)
	s.sentAck = p.InitPktSeq
	s.recvAck2 = p.InitPktSeq
}

func (s *udtSocketRecv) goReceiveEvent() {
	defer s.resendACKTicker.Stop()

	recvEvent := s.recvEvent
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
		case <-s.socket.sockClosed: // socket is closed, leave now
			return
		case <-s.socket.terminateSignal:
			return
		case <-s.resendACKTimer: // handles both resending ACKs and NAKs
			if s.recvAck2.IsLess(s.sentAck) && s.resendACKLimiter.Allow() {
				s.sendACK(s.sentAck)
				s.unackPktCount = 0
			}
			if first, valid := s.recvLossList.FirstSequence(); valid && s.resendNAKLimiter.Allow() {
				s.sendNAK(first, 1)
			}
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
*/

// ingestAck2 is called to process an ACK2 packet
func (s *udtSocketRecv) ingestAck2(p *packet.Ack2Packet, now time.Time) {
	ackHistEntry := s.ackHistory.Remove(p.AckSeqNo) // this also removes all other unacknoweldged ACKs with a lower lastPacket
	if ackHistEntry == nil {
		return // this ACK not found
	}

	s.recvAck2 = ackHistEntry.lastPacket

	s.socket.applyRTT(uint(now.Sub(ackHistEntry.sendTime) / time.Microsecond))

	//s.rto = 4 * s.rtt + s.rttVar
}

// ingestMsgDropReq is called to process an message drop request packet
// This function only makes sense for datagram messages that are OK to be lost. For streaming a file, this makes no sense.
func (s *udtSocketRecv) ingestMsgDropReq(p *packet.MsgDropReqPacket, now time.Time) {
	stopSeq := p.LastSeq.Add(1)
	for pktID := p.FirstSeq; pktID != stopSeq; pktID.Incr() {
		// remove all these packets from the loss list
		s.recvLossList.Remove(pktID.Seq)

		// remove all pending packets with this message
		s.recvPktPend.Remove(pktID.Seq)
	}

	if p.FirstSeq == s.lastSequence.Add(1) {
		s.lastSequence = p.LastSeq
	}
	if s.recvLossList.Count() == 0 {
		s.lastSequence = s.nextSequenceExpect.Add(-1)
	}

	// try to push any pending packets out, now that we have dropped any blocking packets
	for _, nextPkt := range s.recvPktPend.Range(stopSeq, s.nextSequenceExpect) {
		if !s.attemptProcessPacket(nextPkt.pkt, false, false) {
			break
		}
	}
}

// ingestData is called to process a data packet
func (s *udtSocketRecv) ingestData(p *packet.DataPacket, now time.Time) {
	s.socket.cong.onPktRecv(*p)

	/* If the sequence number of the current data packet is 16n + 1,
	where n is an integer, record the time interval between this
	packet and the last data packet in the Packet Pair Window. */
	if (p.Seq.Seq-1)&0xf == 0 {
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
	var ackImmediate bool

	// If the incoming sequence number is greater than the expected one, treat all sequence numbers in the middle as lost (add to lost list) and send a NAK.
	seqDiff := p.Seq.BlindDiff(s.nextSequenceExpect)
	if seqDiff > 0 {
		// Sequence is out of order. Received a higher sequence number than what is expected next.
		for n := uint32(0); n < uint32(seqDiff); n++ {
			s.recvLossList.Add(recvLossEntry{packetID: s.nextSequenceExpect.Add(int32(n))})
		}

		s.sendNAK(s.nextSequenceExpect.Seq, uint32(seqDiff))
		s.nextSequenceExpect = p.Seq.Add(1)
		s.resendNAKLimiter.Reset()

	} else if seqDiff < 0 {
		// If the sequence number is less than LRSN, remove it from the receiver's loss list.
		if !s.recvLossList.Remove(p.Seq.Seq) {
			return // already previously received packet -- ignore
		}
		ackImmediate = true
	} else {
		s.nextSequenceExpect = p.Seq.Add(1)
	}

	if s.socket.isDatagram && p.Seq == s.lastSequence.Add(1) {
		s.lastSequence = p.Seq
		s.ackEvent(false) // Need special sending for datagram, otherwise below code would only send it out after all pieces are received.
	}

	s.attemptProcessPacket(p, true, ackImmediate)
}

func (s *udtSocketRecv) attemptProcessPacket(p *packet.DataPacket, isNew, ackImmediate bool) bool {
	var pieces []*packet.DataPacket
	var success bool

	if s.socket.isDatagram {
		pieces, success = s.reassemblePacketPiecesDatagram(p)
	} else {
		pieces, success = s.reassemblePacketPiecesStream(p)
	}

	if !success {
		// we need to wait for more packets, store and return
		if isNew {
			s.recvPktPend.Add(sendPacketEntry{pkt: p})
		}
		return false
	}

	// If pieces were pulled from the list of packets that were waiting to be processed, remove it now.
	if len(pieces) > 1 {
		for _, piece := range pieces {
			s.recvPktPend.Remove(piece.Seq.Seq)
		}
	}

	s.lastSequence = pieces[len(pieces)-1].Seq
	s.ackEvent(ackImmediate)

	// reassemble the data by appending it from all the pieces
	var msg []byte
	for _, piece := range pieces {
		msg = append(msg, piece.Data...)
	}
	s.messageIn <- msg
	return true
}

// reassemblePacketPiecesDatagram attempts to reassemble a datagram message from multiple pieces
func (s *udtSocketRecv) reassemblePacketPiecesDatagram(p *packet.DataPacket) (pieces []*packet.DataPacket, success bool) {
	boundary, _, msgID := p.GetMessageData()

	// First check if prior packets are needed.
	switch boundary {
	case packet.MbLast, packet.MbMiddle:
		pieceSeq := p.Seq.Add(-1)
		for {
			prevPiece, found := s.recvPktPend.Find(pieceSeq.Seq)
			if !found {
				// we don't have the previous piece, is it missing?
				if s.recvLossList.Find(pieceSeq.Seq) != nil {
					// it's missing, stop processing
					return nil, false
				} else {
				}
				// in any case we can't continue with this
				return nil, false
			}
			prevBoundary, _, prevMsg := prevPiece.pkt.GetMessageData()
			if prevMsg != msgID {
				// ...oops? previous piece isn't in the same message
				return nil, false
			}
			pieces = append([]*packet.DataPacket{prevPiece.pkt}, pieces...)
			if prevBoundary == packet.MbFirst {
				break
			}
			pieceSeq.Decr()
		}
	}

	pieces = append(pieces, p)

	// If more packets are needed, make sure they are available.
	switch boundary {
	case packet.MbFirst, packet.MbMiddle:
		pieceSeq := p.Seq.Add(1)
		for {
			nextPiece, found := s.recvPktPend.Find(pieceSeq.Seq)
			if !found {
				// we don't have the previous piece, is it missing?
				if pieceSeq == s.nextSequenceExpect {
					// hasn't been received yet
					return nil, false
				} else if s.recvLossList.Find(pieceSeq.Seq) != nil {
					// it's missing, stop processing
					return nil, false
				} else {
				}
				// in any case we can't continue with this
				return nil, false
			}
			nextBoundary, _, nextMsg := nextPiece.pkt.GetMessageData()
			if nextMsg != msgID {
				// ...oops? previous piece isn't in the same message
				return nil, false
			}
			pieces = append(pieces, nextPiece.pkt)
			if nextBoundary == packet.MbLast {
				break
			}
		}
	}

	return pieces, true
}

// reassemblePacketPiecesStream tries to see if all remaining packets since the last verified one are buffered (as well as immediately following ones).
func (s *udtSocketRecv) reassemblePacketPiecesStream(p *packet.DataPacket) (pieces []*packet.DataPacket, success bool) {
	// for streams this can continue only if the incoming packet is immediately the next one
	if p.Seq != s.lastSequence.Add(1) {
		return nil, false
	}

	pieces = append(pieces, p)

	// find any other packets that are already buffered
	for nextSeq := p.Seq.Add(1); ; nextSeq.Incr() {
		if nextPacket, found := s.recvPktPend.Find(nextSeq.Seq); found {
			pieces = append(pieces, nextPacket.pkt)
		} else {
			break
		}
	}

	return pieces, true
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
			if sum == 0 { // prevent divide by 0
				sum = time.Millisecond
			}
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

		if sum == 0 { // prevent divide by 0
			sum = time.Millisecond
		}

		bandwidth = int(time.Second * time.Duration(count) / sum)
	}

	return
}

// sendACK sends an ACK with the given sequence number.
func (s *udtSocketRecv) sendACK(ack packet.PacketID) {
	s.sentAck = ack

	s.lastACKID++
	s.ackHistory.Add(ackHistoryEntry{
		ackID:      s.lastACKID,
		lastPacket: ack,
		sendTime:   time.Now(),
	})

	rtt, rttVar := s.socket.getRTT()

	numPendPackets := int(s.nextSequenceExpect.BlindDiff(s.lastSequence) - 1)
	availWindow := int(s.socket.maxFlowWinSize) - numPendPackets
	if availWindow < 2 {
		availWindow = 2
	}

	p := &packet.AckPacket{
		AckSeqNo:  s.lastACKID,
		PktSeqHi:  ack,
		Rtt:       uint32(rtt),
		RttVar:    uint32(rttVar),
		BuffAvail: uint32(availWindow),
	}

	// Send the link info only every SynTime. In theory this should use a mutex, but it does not matter if the link info is sent out multiple times.
	if s.ackLinkInfoSent.IsZero() || time.Since(s.ackLinkInfoSent) >= s.socket.Config.SynTime {
		s.ackLinkInfoSent = time.Now()
		recvSpeed, bandwidth := s.getRcvSpeeds()
		p.IncludeLink = true
		p.PktRecvRate = uint32(recvSpeed)
		p.EstLinkCap = uint32(bandwidth)
	}
	s.sendPacket <- p
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

// ackEvent sends an ACK message if appropriate. It informs the remote peer about the last packet received without loss.
func (s *udtSocketRecv) ackEvent(immediate bool) {
	s.unackPktCount++

	// The ack number is excluding.
	ack := s.lastSequence.Add(1)

	// Only send out the ACK if it represents new information to the remote, i.e. bigger than the last reported number.
	if ack.IsLessEqual(s.sentAck) {
		return
	}

	// Check if the threshold to send is reached, if used. Note that sendACK is called revery SynTime.
	if !immediate {
		ackInterval := uint(s.ackInterval.get())
		if (ackInterval > 0) && (ackInterval > s.unackPktCount) {
			s.sentAck = ack // This is needed for resendACKTimer to pick it up in case no ackInterval count of packets are immediately sent.
			return
		}
	}

	s.sendACK(ack)
	s.unackPktCount = 0
	s.resendACKLimiter.Reset()
}

// rateLimiter is a simple helper to double resending time until reset
// It does not rely on a Ticker which would be expensive.
type rateLimiter struct {
	timeStart time.Time
	wait      time.Duration

	// static variables to be set on init
	MinWaitTime time.Duration
	MaxWaitTime time.Duration

	// Note that wait time is not secured via a mutex or atomic operation.
	// The impact of a race condition is limited and currently does not warrant the overhead of constant locking/unlocking.
}

// Reset sets the initial wait time
func (rate *rateLimiter) Reset() {
	rate.timeStart = time.Now()
	rate.wait = rate.MinWaitTime
}

// Allow checks if the rate allows to send. It will automatically double it if true.
func (rate *rateLimiter) Allow() bool {
	if rate.wait != 0 && time.Now().After(rate.timeStart.Add(rate.wait)) {
		// double the wait time
		rate.wait = rate.wait * 2
		if rate.wait > rate.MaxWaitTime {
			rate.wait = rate.MaxWaitTime
		}
		rate.timeStart = time.Now()

		return true
	}

	return false
}
