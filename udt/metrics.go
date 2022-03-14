package udt

import (
	"github.com/PeernetOfficial/core/udt/packet"
)

// recordTypeOfPacket records statistics on packet related metrics
func (s *UDTSocket) recordTypeOfPacket(p packet.Packet, isSend bool) {

	if isSend {
		switch packet.PacketTypeName(p.PacketType()) {
		case "handshake":
			s.Metrics.PktSendHandShake++
		case "keep-alive":
			s.Metrics.PktSendKeepAlive++
		case "ack":
			s.Metrics.PktSentACK++
		case "nak":
			s.Metrics.PktSentNAK++
		case "congestion":
			s.Metrics.PktSentCongestion++
		case "shutdown":
			s.Metrics.PktSentShutdown++
		case "ack2":
			s.Metrics.PktSentACK2++
		case "msg-drop":
			s.Metrics.PktSendMessageDrop++
		case "error":
			s.Metrics.PktSendError++
		case "user-defined":
			s.Metrics.PktSendUserDefined++
		case "data":
			s.Metrics.PktSentData++
		default:
			s.Metrics.PktSentOther++
		}
	} else {
		switch packet.PacketTypeName(p.PacketType()) {
		case "handshake":
			s.Metrics.PktRecvHandShake++
		case "keep-alive":
			s.Metrics.PktRecvKeepAlive++
		case "ack":
			s.Metrics.PktRecvACK++
		case "nak":
			s.Metrics.PktRecvNAK++
		case "congestion":
			s.Metrics.PktRecvCongestion++
		case "shutdown":
			s.Metrics.PktRecvShutdown++
		case "ack2":
			s.Metrics.PktRecvACK2++
		case "msg-drop":
			s.Metrics.PktRecvMessageDrop++
		case "error":
			s.Metrics.PktRecvError++
		case "user-defined":
			s.Metrics.PktRecvUserDefined++
		case "data":
			s.Metrics.PktRecvData++
		default:
			s.Metrics.PktRecvOther++
		}
	}
}
