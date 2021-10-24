package packet

import (
	"testing"
)

func TestNAKPacket(t *testing.T) {
	pkt1 := &NakPacket{
		CmpLossInfo: []uint32{90},
	}
	pkt1.SetHeader(59, 100)
	testPacket(pkt1, t)
}
