package udt

import (
	"fmt"
	"github.com/PeernetOfficial/core/udt/packet"
)

// Performance metrics
var (
	PktSent                      uint64  // number of sent data packets, including retransmissions
	PktSendHandShake             uint64  // number of Handshake packets sent
	PktRecvHandShake             uint64  // number of Handshake packets received
	PktSendKeepAlive             uint64  // number of Keep-alive packets sent
	PktRecvKeepAlive             uint64  // number of Keep-alive packets received
	PktRecv                      uint64  // number of received packets
	PktSentCongestion            uint64  // number of Congestion Packets sent
	PktRecvCongestion            uint64  // number of Congestion Packets received
	PktSentShutdown              uint64  // number of Shutdown Packets sent
	PktRecvShutdown              uint64  // number of Shutdown Packets received
	PktSendMessageDrop           uint64  // number of Message Drop Packets sent
	PktRecvMessageDrop           uint64  // number of Message Drop Packets received
	PktSendError                 uint64  // number of Error Packets sent
	PktRecvError                 uint64  // number of Error Packets received
	PktSendUserDefined           uint64  // number of User Defined Packets sent
	PktRecvUserDefined           uint64  // number of User Defined Packets received
	PktSndLoss                   uint    // number of lost packets (sender side)
	PktRcvLoss                   uint    // number of lost packets (receiver side)
	PktRetrans                   uint    // number of retransmitted packets
	PktSentACK                   uint    // number of sent ACK packets
	PktSentACK2                  uint    // number of sent ACK2 packets
	PktRecvACK2                  uint    // number of received ACK2 packets
	PktRecvACK                   uint    // number of received ACK packets
	PktSentNAK                   uint    // number of sent NAK packets
	PktRecvNAK                   uint    // number of received NAK packets
	PktSentOther                 uint    // number of sent Other packets
	PktRecvOther                 uint    // number of received Other packets
	MbpsSendRate                 float64 // sending rate in Mb/s
	MbpsRecvRate                 float64 // receiving rate in Mb/s
	DataPacketsAttemptedProcess  int     // Tracking how many data packets are attempted to get processed
	DataPacketsNotFullyProcessed int     // Tracking how many data packets are not fully processed
)

// PrintTypeOfPacket This is for metrics purposes
// Prints the type of packet transmitted.
func (s *UDTSocket) PrintTypeOfPacket(p packet.Packet, Type string) {

	if packet.PacketTypeName(p.PacketType()) == "handshake" {
		//m.socket.PktSendHandShake = 0
		//// Increment the number of handshake packets sent
		if Type == "send" {
			s.metrics.PktSendHandShake = s.metrics.PktSendHandShake + 1
		} else {
			s.metrics.PktRecvHandShake = s.metrics.PktRecvHandShake + 1
		}
		//m.socket.PktSendHandShake = m.socket.PktSendHandShake + 1
		//fmt.Println("handshake")
	} else if packet.PacketTypeName(p.PacketType()) == "keep-alive" {
		//m.socket.PktSendKeepAlive = 0
		//// Increment the number of keep-alive packets sent
		if Type == "send" {
			s.metrics.PktSendKeepAlive = s.metrics.PktSendKeepAlive + 1
		} else {
			s.metrics.PktRecvKeepAlive = s.metrics.PktRecvKeepAlive + 1
		}
		//m.socket.PktSendKeepAlive = m.socket.PktSendKeepAlive + 1
		//fmt.Println("keep-alive")
	} else if packet.PacketTypeName(p.PacketType()) == "ack" {
		//m.socket.PktSentACK = 0
		//// Increment the number of ack packets sent
		if Type == "send" {
			s.metrics.PktSentACK = s.metrics.PktSentACK + 1
		} else {
			s.metrics.PktRecvACK = s.metrics.PktRecvACK + 1
		}
		//m.socket.PktSentACK = m.socket.PktSentACK + 1
		//fmt.Println("ack")
	} else if packet.PacketTypeName(p.PacketType()) == "nak" {
		//m.socket.PktSentNAK = 0
		//// Increment the number of nak packets sent
		if Type == "send" {
			s.metrics.PktSentNAK = s.metrics.PktSentNAK + 1
		} else {
			s.metrics.PktRecvNAK = s.metrics.PktRecvNAK + 1
		}
		//m.socket.PktSentNAK = m.socket.PktSentNAK + 1
		// fmt.Println("nak")
	} else if packet.PacketTypeName(p.PacketType()) == "congestion" {
		//m.socket.PktSentCongestion = 0
		//// Increment the number of congestion packets sent
		if Type == "send" {
			s.metrics.PktSentCongestion = s.metrics.PktSentCongestion + 1
		} else {
			s.metrics.PktRecvCongestion = s.metrics.PktRecvCongestion + 1
		}
		//m.socket.PktSentCongestion = m.socket.PktSentCongestion + 1
		// fmt.Println("congestion")
	} else if packet.PacketTypeName(p.PacketType()) == "shutdown" {
		//m.socket.PktSentShutdown = 0
		//// Increment the number of shutdown packets sent
		if Type == "send" {
			s.metrics.PktSentShutdown = s.metrics.PktSentShutdown + 1
		} else {
			s.metrics.PktRecvShutdown = s.metrics.PktRecvShutdown + 1
		}
		//m.socket.PktSentShutdown = m.socket.PktSentShutdown + 1
	} else if packet.PacketTypeName(p.PacketType()) == "ack2" {
		//m.socket.PktSentACK2 = 0
		//// Increment the number of ack2 packets sent
		if Type == "send" {
			s.metrics.PktSentACK2 = s.metrics.PktSentACK2 + 1
		} else {
			s.metrics.PktRecvACK2 = s.metrics.PktRecvACK2 + 1
		}
		//m.socket.PktSentACK2 = m.socket.PktSentACK2 + 1
		//fmt.Println("ack")
	} else if packet.PacketTypeName(p.PacketType()) == "msg-drop" {
		//m.socket.PktSendMessageDrop = 0
		//// Increment the number of msg-drop packets sent
		if Type == "send" {
			s.metrics.PktSendMessageDrop = s.metrics.PktSendMessageDrop + 1
		} else {
			s.metrics.PktRecvMessageDrop = s.metrics.PktRecvMessageDrop + 1
		}
		//m.socket.PktSendMessageDrop = m.socket.PktSendMessageDrop + 1
		//fmt.Println("msg-drop")
	} else if packet.PacketTypeName(p.PacketType()) == "error" {
		//m.socket.PktSendError = 0
		//// Increment the number of error packets sent
		if Type == "send" {
			s.metrics.PktSendError = s.metrics.PktSendError + 1
		} else {
			s.metrics.PktRecvError = s.metrics.PktRecvError + 1
		}
		//m.socket.PktSendError = m.socket.PktSendError + 1
		//fmt.Println("error")
	} else if packet.PacketTypeName(p.PacketType()) == "user-defined" {
		//m.socket.PktSendUserDefined = 0
		//// Increment the number of user-defined packets sent
		if Type == "send" {
			s.metrics.PktSendUserDefined = s.metrics.PktSendUserDefined + 1
		} else {
			s.metrics.PktRecvUserDefined = s.metrics.PktRecvUserDefined + 1
		}
		//m.socket.PktSendUserDefined = m.socket.PktSendUserDefined + 1
		//fmt.Println("user-defined")
	} else if packet.PacketTypeName(p.PacketType()) == "data" {
		//m.socket.PktSent = 0
		//// Increment the number of data packets sent
		if Type == "send" {
			s.metrics.PktSent = s.metrics.PktSent + 1
		} else {
			s.metrics.PktRecv = s.metrics.PktRecv + 1
		}
		//m.socket.PktSent = m.socket.PktSent + 1
		// fmt.Println("data")
	} else {
		if Type == "send" {
			s.metrics.PktSentOther = s.metrics.PktSentOther + 1
		} else {
			s.metrics.PktRecvOther = s.metrics.PktRecvOther + 1
		}
	}
}

func (s *UDTSocket) IncrementDataPacketsAttemptedProcess() {
	s.metrics.DataPacketsAttemptedProcess++
}

func (s *UDTSocket) IncrementDataPacketsNotFullyProcessed() {
	s.metrics.DataPacketsNotFullyProcessed++
}

// PrintMetrics Prints metrics collected
func (s *UDTSocket) PrintMetrics() {
	fmt.Printf("Total HandShake Packets Sent:%d \n", s.metrics.PktSendHandShake)
	fmt.Printf("Total HandShake Packets Received:%d \n", s.metrics.PktRecvHandShake)

	fmt.Printf("Total keep-alive Packets Sent:%d \n", s.metrics.PktSendKeepAlive)
	fmt.Printf("Total keep-alive Packets Received:%d \n", s.metrics.PktRecvKeepAlive)

	fmt.Printf("Total ACK Packets Sent:%d \n", s.metrics.PktSentACK)
	fmt.Printf("Total ACK Packets Received:%d \n", s.metrics.PktRecvACK)

	fmt.Printf("Total NAK Packets Sent:%d \n", s.metrics.PktSentNAK)
	fmt.Printf("Total NAK Packets Received:%d \n", s.metrics.PktRecvNAK)

	fmt.Printf("Total Congestion Packets Sent:%d \n", s.metrics.PktSentCongestion)
	fmt.Printf("Total Congestion Packets Received:%d \n", s.metrics.PktRecvCongestion)

	fmt.Printf("Total Shutdown Packets Sent:%d \n", s.metrics.PktSentShutdown)
	fmt.Printf("Total Shutdown Packets Received:%d \n", s.metrics.PktRecvShutdown)

	fmt.Printf("Total ACK2 Packets Sent:%d \n", s.metrics.PktSentACK2)
	fmt.Printf("Total ACK2 Packets Received:%d \n", s.metrics.PktRecvACK2)

	fmt.Printf("Total Msg-drop Packets Sent:%d \n", s.metrics.PktSendMessageDrop)
	fmt.Printf("Total Msg-drop Packets Received:%d \n", s.metrics.PktRecvMessageDrop)

	fmt.Printf("Total Error Packets Sent:%d \n", s.metrics.PktSendError)
	fmt.Printf("Total Error Packets Received:%d \n", s.metrics.PktRecvError)

	fmt.Printf("Total User-defined Packets Sent:%d \n", s.metrics.PktSendUserDefined)
	fmt.Printf("Total User-define Packets Received:%d \n", s.metrics.PktRecvUserDefined)

	fmt.Printf("Total Data Packets Sent:%d \n", s.metrics.PktSent)
	fmt.Printf("Total Data Received:%d \n", s.metrics.PktRecv)

	fmt.Printf("Total Other Packets Sent:%d \n", s.metrics.PktSentOther)
	fmt.Printf("Total Other Packets Received:%d \n", s.metrics.PktRecvOther)

	fmt.Printf("Total Number Of Data packets attempted to get Processed:%d \n", s.metrics.DataPacketsAttemptedProcess)
	fmt.Printf("Total Number Of Data packets out of order:%d \n", s.metrics.DataPacketsNotFullyProcessed)
}
