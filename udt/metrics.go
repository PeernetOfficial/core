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
func PrintTypeOfPacket(p packet.Packet, Type string) {

	if packet.PacketTypeName(p.PacketType()) == "handshake" {
		//m.socket.PktSendHandShake = 0
		//// Increment the number of handshake packets sent
		if Type == "send" {
			PktSendHandShake = PktSendHandShake + 1
		} else {
			PktRecvHandShake = PktRecvHandShake + 1
		}
		//m.socket.PktSendHandShake = m.socket.PktSendHandShake + 1
		//fmt.Println("handshake")
	} else if packet.PacketTypeName(p.PacketType()) == "keep-alive" {
		//m.socket.PktSendKeepAlive = 0
		//// Increment the number of keep-alive packets sent
		if Type == "send" {
			PktSendKeepAlive = PktSendKeepAlive + 1
		} else {
			PktRecvKeepAlive = PktRecvKeepAlive + 1
		}
		//m.socket.PktSendKeepAlive = m.socket.PktSendKeepAlive + 1
		//fmt.Println("keep-alive")
	} else if packet.PacketTypeName(p.PacketType()) == "ack" {
		//m.socket.PktSentACK = 0
		//// Increment the number of ack packets sent
		if Type == "send" {
			PktSentACK = PktSentACK + 1
		} else {
			PktRecvACK = PktRecvACK + 1
		}
		//m.socket.PktSentACK = m.socket.PktSentACK + 1
		//fmt.Println("ack")
	} else if packet.PacketTypeName(p.PacketType()) == "nak" {
		//m.socket.PktSentNAK = 0
		//// Increment the number of nak packets sent
		if Type == "send" {
			PktSentNAK = PktSentNAK + 1
		} else {
			PktRecvNAK = PktRecvNAK + 1
		}
		//m.socket.PktSentNAK = m.socket.PktSentNAK + 1
		// fmt.Println("nak")
	} else if packet.PacketTypeName(p.PacketType()) == "congestion" {
		//m.socket.PktSentCongestion = 0
		//// Increment the number of congestion packets sent
		if Type == "send" {
			PktSentCongestion = PktSentCongestion + 1
		} else {
			PktRecvCongestion = PktRecvCongestion + 1
		}
		//m.socket.PktSentCongestion = m.socket.PktSentCongestion + 1
		// fmt.Println("congestion")
	} else if packet.PacketTypeName(p.PacketType()) == "shutdown" {
		//m.socket.PktSentShutdown = 0
		//// Increment the number of shutdown packets sent
		if Type == "send" {
			PktSentShutdown = PktSentShutdown + 1
		} else {
			PktRecvShutdown = PktRecvShutdown + 1
		}
		//m.socket.PktSentShutdown = m.socket.PktSentShutdown + 1
		fmt.Println("shutdown")
	} else if packet.PacketTypeName(p.PacketType()) == "ack2" {
		//m.socket.PktSentACK2 = 0
		//// Increment the number of ack2 packets sent
		if Type == "send" {
			PktSentACK2 = PktSentACK2 + 1
		} else {
			PktRecvACK2 = PktRecvACK2 + 1
		}
		//m.socket.PktSentACK2 = m.socket.PktSentACK2 + 1
		//fmt.Println("ack")
	} else if packet.PacketTypeName(p.PacketType()) == "msg-drop" {
		//m.socket.PktSendMessageDrop = 0
		//// Increment the number of msg-drop packets sent
		if Type == "send" {
			PktSendMessageDrop = PktSendMessageDrop + 1
		} else {
			PktRecvMessageDrop = PktRecvMessageDrop + 1
		}
		//m.socket.PktSendMessageDrop = m.socket.PktSendMessageDrop + 1
		//fmt.Println("msg-drop")
	} else if packet.PacketTypeName(p.PacketType()) == "error" {
		//m.socket.PktSendError = 0
		//// Increment the number of error packets sent
		if Type == "send" {
			PktSendError = PktSendError + 1
		} else {
			PktRecvError = PktRecvError + 1
		}
		//m.socket.PktSendError = m.socket.PktSendError + 1
		//fmt.Println("error")
	} else if packet.PacketTypeName(p.PacketType()) == "user-defined" {
		//m.socket.PktSendUserDefined = 0
		//// Increment the number of user-defined packets sent
		if Type == "send" {
			PktSendUserDefined = PktSendUserDefined + 1
		} else {
			PktRecvUserDefined = PktRecvUserDefined + 1
		}
		//m.socket.PktSendUserDefined = m.socket.PktSendUserDefined + 1
		//fmt.Println("user-defined")
	} else if packet.PacketTypeName(p.PacketType()) == "data" {
		//m.socket.PktSent = 0
		//// Increment the number of data packets sent
		if Type == "send" {
			PktSent = PktSent + 1
		} else {
			PktRecv = PktRecv + 1
		}
		//m.socket.PktSent = m.socket.PktSent + 1
		// fmt.Println("data")
	} else {
		if Type == "send" {
			PktSentOther = PktSentOther + 1
		} else {
			PktRecvOther = PktRecvOther + 1
		}
	}
}

func IncrementDataPacketsAttemptedProcess() {
	DataPacketsAttemptedProcess++
}

func IncrementDataPacketsNotFullyProcessed() {
	DataPacketsNotFullyProcessed++
}

// PrintMetrics Prints metrics collected
func PrintMetrics() {
	fmt.Printf("Total HandShake Packets Sent:%d \n", PktSendHandShake)
	fmt.Printf("Total HandShake Packets Received:%d \n", PktRecvHandShake)

	fmt.Printf("Total keep-alive Packets Sent:%d \n", PktSendKeepAlive)
	fmt.Printf("Total keep-alive Packets Received:%d \n", PktRecvKeepAlive)

	fmt.Printf("Total ACK Packets Sent:%d \n", PktSentACK)
	fmt.Printf("Total ACK Packets Received:%d \n", PktRecvACK)

	fmt.Printf("Total NAK Packets Sent:%d \n", PktSentNAK)
	fmt.Printf("Total NAK Packets Received:%d \n", PktRecvNAK)

	fmt.Printf("Total Congestion Packets Sent:%d \n", PktSentCongestion)
	fmt.Printf("Total Congestion Packets Received:%d \n", PktRecvCongestion)

	fmt.Printf("Total Shutdown Packets Sent:%d \n", PktSentShutdown)
	fmt.Printf("Total Shutdown Packets Received:%d \n", PktRecvShutdown)

	fmt.Printf("Total ACK2 Packets Sent:%d \n", PktSentACK2)
	fmt.Printf("Total ACK2 Packets Received:%d \n", PktRecvACK2)

	fmt.Printf("Total Msg-drop Packets Sent:%d \n", PktSendMessageDrop)
	fmt.Printf("Total Msg-drop Packets Received:%d \n", PktRecvMessageDrop)

	fmt.Printf("Total Error Packets Sent:%d \n", PktSendError)
	fmt.Printf("Total Error Packets Received:%d \n", PktRecvError)

	fmt.Printf("Total User-defined Packets Sent:%d \n", PktSendUserDefined)
	fmt.Printf("Total User-define Packets Received:%d \n", PktRecvUserDefined)

	fmt.Printf("Total Data Packets Sent:%d \n", PktSent)
	fmt.Printf("Total Data Received:%d \n", PktRecv)

	fmt.Printf("Total Other Packets Sent:%d \n", PktSentOther)
	fmt.Printf("Total Other Packets Received:%d \n", PktRecvOther)

	fmt.Printf("Total Number Of Data packets attempted to get Processed:%d \n", DataPacketsAttemptedProcess)
	fmt.Printf("Total Number Of Data packets not fully processed:%d \n", DataPacketsNotFullyProcessed)
}

func ResetMetrics() {

}
