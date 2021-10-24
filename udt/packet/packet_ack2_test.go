package packet

import (
	"testing"
)

func TestACK2Packet(t *testing.T) {
	pkt1 := &Ack2Packet{
		AckSeqNo: 90,
	}
	pkt1.SetHeader(59, 100)
	testPacket(pkt1, t)
}
