package udt

import (
	"time"

	"github.com/PeernetOfficial/core/udt/packet"
)

// CongestionControlParms permits a CongestionControl implementation to interface with the UDT socket
type CongestionControlParms interface {
	// GetSndCurrSeqNo is the most recently sent packet ID
	GetSndCurrSeqNo() packet.PacketID

	// SetCongestionWindowSize sets the size of the congestion window (in packets)
	SetCongestionWindowSize(uint)

	// GetCongestionWindowSize gets the size of the congestion window (in packets)
	GetCongestionWindowSize() uint

	// GetPacketSendPeriod gets the current delay between sending packets
	GetPacketSendPeriod() time.Duration

	// SetPacketSendPeriod sets the current delay between sending packets
	SetPacketSendPeriod(time.Duration)

	// GetMaxFlowWindow is the largest number of unacknowledged packets we can receive (in packets)
	GetMaxFlowWindow() uint

	// GetReceiveRates is the current calculated receive rate and bandwidth (in packets/sec)
	GetReceiveRates() (recvSpeed, bandwidth uint)

	// GetRTT is the current calculated roundtrip time between peers
	GetRTT() time.Duration

	// GetMSS is the largest packet size we can currently send (in bytes)
	GetMSS() uint

	// SetACKPerid sets the time between ACKs sent to the peer
	SetACKPeriod(time.Duration)

	// SetRTOPeriod overrides the default EXP timeout calculations waiting for data from the peer
	SetRTOPeriod(time.Duration)
}

// CongestionControl controls how timing is handled and UDT connections tuned
type CongestionControl interface {
	// Init to be called (only) at the start of a UDT connection.
	Init(CongestionControlParms, time.Duration)

	// Close to be called when a UDT connection is closed.
	Close(CongestionControlParms)

	// OnACK to be called when an ACK packet is received
	OnACK(CongestionControlParms, packet.PacketID)

	// OnNAK to be called when a loss report is received
	OnNAK(CongestionControlParms, []packet.PacketID)

	// OnTimeout to be called when a timeout event occurs
	OnTimeout(CongestionControlParms)

	// OnPktSent to be called when data is sent
	OnPktSent(CongestionControlParms, packet.Packet)

	// OnPktRecv to be called when data is received
	OnPktRecv(CongestionControlParms, packet.DataPacket)

	// OnCustomMsg to process a user-defined packet
	OnCustomMsg(CongestionControlParms, packet.UserDefControlPacket)
}
