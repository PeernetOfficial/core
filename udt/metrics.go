package udt

import (
	"github.com/PeernetOfficial/core/udt/packet"
)

// RecordTypeOfPacket This is for metrics purposes
// Prints the type of packet transmitted.
func (s *UDTSocket) RecordTypeOfPacket(p packet.Packet, Type string) {

	switch packet.PacketTypeName(p.PacketType()) {
	case "handshake":
		if Type == "send" {
			s.metrics.PktSendHandShake++
		} else {
			s.metrics.PktRecvHandShake++
		}
	case "keep-alive":
		if Type == "send" {
			s.metrics.PktSendKeepAlive++
		} else {
			s.metrics.PktRecvKeepAlive++
		}
	case "ack":
		if Type == "send" {
			s.metrics.PktSentACK++
		} else {
			s.metrics.PktRecvACK++
		}
	case "nak":
		if Type == "send" {
			s.metrics.PktSentNAK++
		} else {
			s.metrics.PktRecvNAK++
		}
	case "congestion":
		if Type == "send" {
			s.metrics.PktSentCongestion++
		} else {
			s.metrics.PktRecvCongestion++
		}
	case "shutdown":
		if Type == "send" {
			s.metrics.PktSentShutdown++
		} else {
			s.metrics.PktRecvShutdown++
		}
	case "ack2":
		if Type == "send" {
			s.metrics.PktSentACK2++
		} else {
			s.metrics.PktRecvACK2++
		}
	case "msg-drop":
		if Type == "send" {
			s.metrics.PktSendMessageDrop++
		} else {
			s.metrics.PktRecvMessageDrop++
		}
	case "error":
		if Type == "send" {
			s.metrics.PktSendError++
		} else {
			s.metrics.PktRecvError++
		}
	case "user-defined":
		if Type == "send" {
			s.metrics.PktSendUserDefined++
		} else {
			s.metrics.PktRecvUserDefined++
		}
	case "data":
		if Type == "send" {
			s.metrics.PktSent++
		} else {
			s.metrics.PktRecv++
		}
	default:
		if Type == "send" {
			s.metrics.PktSentOther++
		} else {
			s.metrics.PktRecvOther++
		}
	}
}

func (s *UDTSocket) IncrementDataPacketsAttemptedProcess() {
	s.metrics.DataPacketsAttemptedProcess++
}

func (s *UDTSocket) IncrementDataPacketsNotFullyProcessed() {
	s.metrics.DataPacketsNotFullyProcessed++
}

// GetMetrics Returns the UDT metrics
func (s *UDTSocket) GetMetrics() *Metrics {
	return s.metrics
}
