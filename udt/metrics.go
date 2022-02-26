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

// PrintMetrics Prints metrics collected
//func (s *UDTSocket) PrintMetrics() {
//	fmt.Printf("Total HandShake Packets Sent:%d \n", s.metrics.PktSendHandShake)
//	fmt.Printf("Total HandShake Packets Received:%d \n", s.metrics.PktRecvHandShake)
//
//	fmt.Printf("Total keep-alive Packets Sent:%d \n", s.metrics.PktSendKeepAlive)
//	fmt.Printf("Total keep-alive Packets Received:%d \n", s.metrics.PktRecvKeepAlive)
//
//	fmt.Printf("Total ACK Packets Sent:%d \n", s.metrics.PktSentACK)
//	fmt.Printf("Total ACK Packets Received:%d \n", s.metrics.PktRecvACK)
//
//	fmt.Printf("Total NAK Packets Sent:%d \n", s.metrics.PktSentNAK)
//	fmt.Printf("Total NAK Packets Received:%d \n", s.metrics.PktRecvNAK)
//
//	fmt.Printf("Total Congestion Packets Sent:%d \n", s.metrics.PktSentCongestion)
//	fmt.Printf("Total Congestion Packets Received:%d \n", s.metrics.PktRecvCongestion)
//
//	fmt.Printf("Total Shutdown Packets Sent:%d \n", s.metrics.PktSentShutdown)
//	fmt.Printf("Total Shutdown Packets Received:%d \n", s.metrics.PktRecvShutdown)
//
//	fmt.Printf("Total ACK2 Packets Sent:%d \n", s.metrics.PktSentACK2)
//	fmt.Printf("Total ACK2 Packets Received:%d \n", s.metrics.PktRecvACK2)
//
//	fmt.Printf("Total Msg-drop Packets Sent:%d \n", s.metrics.PktSendMessageDrop)
//	fmt.Printf("Total Msg-drop Packets Received:%d \n", s.metrics.PktRecvMessageDrop)
//
//	fmt.Printf("Total Error Packets Sent:%d \n", s.metrics.PktSendError)
//	fmt.Printf("Total Error Packets Received:%d \n", s.metrics.PktRecvError)
//
//	fmt.Printf("Total User-defined Packets Sent:%d \n", s.metrics.PktSendUserDefined)
//	fmt.Printf("Total User-define Packets Received:%d \n", s.metrics.PktRecvUserDefined)
//
//	fmt.Printf("Total Data Packets Sent:%d \n", s.metrics.PktSent)
//	fmt.Printf("Total Data Received:%d \n", s.metrics.PktRecv)
//
//	fmt.Printf("Total Other Packets Sent:%d \n", s.metrics.PktSentOther)
//	fmt.Printf("Total Other Packets Received:%d \n", s.metrics.PktRecvOther)
//
//	fmt.Printf("Total Number Of Data packets attempted to get Processed:%d \n", s.metrics.DataPacketsAttemptedProcess)
//	fmt.Printf("Total Number Of Data packets out of order:%d \n", s.metrics.DataPacketsNotFullyProcessed)
//}
