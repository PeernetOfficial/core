package udt

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/PeernetOfficial/core/udt/packet"
)

// NativeCongestionControl implements the default congestion control logic for UDP
type NativeCongestionControl struct {
	rcInterval    time.Duration   // UDT Rate control interval
	lastRCTime    time.Time       // last rate increase time
	slowStart     bool            // if in slow start phase
	lastAck       packet.PacketID // last ACKed seq no
	loss          bool            // if loss happened since last rate increase
	lastDecSeq    packet.PacketID // biggest sequence number when last time the packet sending rate is decreased
	lastDecPeriod time.Duration   // value of PacketSendPeriod when last decrease happened
	nakCount      int             // current number of NAKs in the current period
	decRandom     int             // random threshold on decrease by number of loss events
	avgNAKNum     int             // average number of NAKs in a congestion period
	decCount      int             // number of decreases in a congestion epoch
}

// Init to be called (only) at the start of a UDT connection.
func (ncc NativeCongestionControl) Init(parms CongestionControlParms, synTime time.Duration) {
	ncc.rcInterval = synTime
	ncc.lastRCTime = time.Now()
	parms.SetACKPeriod(ncc.rcInterval)

	ncc.slowStart = true
	ncc.lastAck = parms.GetSndCurrSeqNo()
	ncc.loss = false
	ncc.lastDecSeq = ncc.lastAck.Add(-1)
	ncc.lastDecPeriod = 1 * time.Microsecond
	ncc.avgNAKNum = 0
	ncc.nakCount = 0
	ncc.decRandom = 1

	parms.SetCongestionWindowSize(16)
	parms.SetPacketSendPeriod(1 * time.Microsecond)
}

// Close to be called when a UDT connection is closed.
func (ncc NativeCongestionControl) Close(parms CongestionControlParms) {
	// nothing done for this event
}

// OnACK to be called when an ACK packet is received
func (ncc NativeCongestionControl) OnACK(parms CongestionControlParms, ack packet.PacketID) {
	currTime := time.Now()
	if currTime.Sub(ncc.lastRCTime) < ncc.rcInterval {
		return
	}
	ncc.lastRCTime = currTime
	cWndSize := parms.GetCongestionWindowSize()
	pktSendPeriod := parms.GetPacketSendPeriod()
	recvRate, bandwidth := parms.GetReceiveRates()
	rtt := parms.GetRTT()

	// If the current status is in the slow start phase, set the congestion window
	// size to the product of packet arrival rate and (RTT + SYN). Slow Start ends. Stop.
	if ncc.slowStart {
		fmt.Println("slow start")
		cWndSize = uint(int(cWndSize) + int(ack.BlindDiff(ncc.lastAck)))
		ncc.lastAck = ack

		if cWndSize > parms.GetMaxFlowWindow() {
			ncc.slowStart = false
			if recvRate > 0 {
				parms.SetPacketSendPeriod(time.Second / time.Duration(recvRate))
			} else {
				parms.SetPacketSendPeriod((rtt + ncc.rcInterval) / time.Duration(cWndSize))
			}
		} else {
			// During Slow Start, no rate increase
			parms.SetCongestionWindowSize(cWndSize)
			return
		}
	} else {
		// Set the congestion window size (CWND) to: CWND = A * (RTT + SYN) + 16.
		cWndSize = uint((float64(recvRate)/float64(time.Second))*float64(rtt+ncc.rcInterval) + 16)
		parms.SetCongestionWindowSize(cWndSize)
		fmt.Println(cWndSize)
	}
	if ncc.loss {
		ncc.loss = false
		parms.SetCongestionWindowSize(cWndSize)
		fmt.Println("ncc loss")
		//fmt.Println(parms.GetCongestionWindowSize())
		return
	}
	/*
		The number of sent packets to be increased in the next SYN period
		(inc) is calculated as:
		   if (B <= C)
			  inc = 1/PS;
		   else
			  inc = max(10^(ceil(log10((B-C)*PS*8))) * Beta/PS, 1/PS);
		where B is the estimated link capacity and C is the current
		sending speed. All are counted as packets per second. PS is the
		fixed size of UDT packet counted in bytes. Beta is a constant
		value of 0.0000015.
	*/

	// Note: 1/24/2012
	// The minimum increase parameter is increased from "1.0 / m_iMSS" to 0.01
	// because the original was too small and caused sending rate to stay at low level
	// for long time.
	var inc float64
	const minInc float64 = 0.01

	if pktSendPeriod == 0 { // fix divide by zero
		pktSendPeriod = time.Nanosecond * 10
	}

	B := time.Duration(bandwidth) - time.Second/time.Duration(pktSendPeriod)
	bandwidth9 := time.Duration(bandwidth / 9)
	if (pktSendPeriod > ncc.lastDecPeriod) && (bandwidth9 < B) {
		B = bandwidth9
	}
	if B <= 0 {
		inc = minInc
	} else {
		// inc = max(10 ^ ceil(log10( B * MSS * 8 ) * Beta / MSS, 1/MSS)
		// Beta = 1.5 * 10^(-6)

		mss := parms.GetMSS()
		inc = math.Pow10(int(math.Ceil(math.Log10(float64(B)*float64(mss)*8.0)))) * 0.0000015 / float64(mss)

		if inc < minInc {
			inc = minInc
		}
	}

	// The SND period is updated as: SND = (SND * SYN) / (SND * inc + SYN).
	parms.SetPacketSendPeriod(time.Duration(float64(pktSendPeriod*ncc.rcInterval) / (float64(pktSendPeriod)*inc + float64(ncc.rcInterval))))
}

// OnNAK to be called when a loss report is received
func (ncc NativeCongestionControl) OnNAK(parms CongestionControlParms, losslist []packet.PacketID) {
	// If it is in slow start phase, set inter-packet interval to 1/recvrate. Slow start ends. Stop.
	if ncc.slowStart {
		ncc.slowStart = false
		recvRate, _ := parms.GetReceiveRates()
		if recvRate > 0 {
			// Set the sending rate to the receiving rate.
			parms.SetPacketSendPeriod(time.Second / time.Duration(recvRate))
			return
		}
		// If no receiving rate is observed, we have to compute the sending
		// rate according to the current window size, and decrease it
		// using the method below.
		parms.SetPacketSendPeriod(time.Duration(float64(time.Microsecond) * float64(parms.GetCongestionWindowSize()) / float64(parms.GetRTT()+ncc.rcInterval)))
	}

	ncc.loss = true

	/*
		2) If this NAK starts a new congestion period, increase inter-packet
			interval (snd) to snd = snd * 1.125; Update AvgNAKNum, reset
			NAKCount to 1, and compute DecRandom to a random (average
			distribution) number between 1 and AvgNAKNum. Update LastDecSeq.
			Stop.
		3) If DecCount <= 5, and NAKCount == DecCount * DecRandom:
			a. Update SND period: SND = SND * 1.125;
			b. Increase DecCount by 1;
			c. Record the current largest sent sequence number (LastDecSeq).
	*/
	pktSendPeriod := parms.GetPacketSendPeriod()
	if len(losslist) > 0 && ncc.lastDecSeq.BlindDiff(losslist[0]) > 0 {
		ncc.lastDecPeriod = pktSendPeriod
		parms.SetPacketSendPeriod(pktSendPeriod * 1125 / 1000)

		ncc.avgNAKNum = int(math.Ceil(float64(ncc.avgNAKNum)*0.875 + float64(ncc.nakCount)*0.125))
		ncc.nakCount = 1
		ncc.decCount = 1

		ncc.lastDecSeq = parms.GetSndCurrSeqNo()

		// remove global synchronization using randomization
		rand := float64(rand.Uint32()) / math.MaxUint32
		ncc.decRandom = int(math.Ceil(float64(ncc.avgNAKNum) * rand))
		if ncc.decRandom < 1 {
			ncc.decRandom = 1
		}
	} else {
		if ncc.decCount < 5 {
			ncc.nakCount++
			if ncc.decRandom != 0 && ncc.nakCount%ncc.decRandom != 0 {
				ncc.decCount++
				return
			}
		}
		ncc.decCount++

		// 0.875^5 = 0.51, rate should not be decreased by more than half within a congestion period
		parms.SetPacketSendPeriod(pktSendPeriod * 1125 / 1000)
		ncc.lastDecSeq = parms.GetSndCurrSeqNo()
	}

	//fmt.Println(parms.GetMaxFlowWindow())
}

// OnTimeout to be called when a timeout event occurs
func (ncc NativeCongestionControl) OnTimeout(parms CongestionControlParms) {
	if ncc.slowStart {
		ncc.slowStart = false
		recvRate, _ := parms.GetReceiveRates()
		if recvRate > 0 {
			parms.SetPacketSendPeriod(time.Second / time.Duration(recvRate))
		} else {
			parms.SetPacketSendPeriod(time.Duration(float64(time.Microsecond) * float64(parms.GetCongestionWindowSize()) / float64(parms.GetRTT()+ncc.rcInterval)))
		}
	} else {
		/*
			pktSendPeriod := parms.GetPacketSendPeriod()
			ncc.lastDecPeriod = pktSendPeriod
			parms.SetPacketSendPeriod(math.Ceil(pktSendPeriod * 2))
			ncc.lastDecSeq = ncc.lastAck
		*/
	}
}

// OnPktSent to be called when data is sent
func (ncc NativeCongestionControl) OnPktSent(parms CongestionControlParms, pkt packet.Packet) {
	// nothing done for this event
}

// OnPktRecv to be called when a data is received
func (ncc NativeCongestionControl) OnPktRecv(parms CongestionControlParms, pkt packet.DataPacket) {
	// nothing done for this event
}

// OnCustomMsg to process a user-defined packet
func (ncc NativeCongestionControl) OnCustomMsg(parms CongestionControlParms, pkt packet.UserDefControlPacket) {
	// nothing done for this event
}
