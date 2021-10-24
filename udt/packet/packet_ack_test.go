package packet

import (
	"testing"
)

func TestACKPacket(t *testing.T) {
	pkt1 := &AckPacket{
		AckSeqNo:    90,
		PktSeqHi:    PacketID{Seq: 91},
		Rtt:         92,
		RttVar:      93,
		BuffAvail:   94,
		IncludeLink: true,
		PktRecvRate: 95,
		EstLinkCap:  96,
	}
	pkt1.SetHeader(59, 100)
	testPacket(pkt1, t)

	pkt2 := &AckPacket{
		AckSeqNo:    90,
		PktSeqHi:    PacketID{Seq: 91},
		Rtt:         92,
		RttVar:      93,
		BuffAvail:   94,
		IncludeLink: false,
	}
	pkt2.SetHeader(59, 100)
	testPacket(pkt2, t)
}
