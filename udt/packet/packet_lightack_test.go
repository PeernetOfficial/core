package packet

import (
	"testing"
)

func TestLightAckPacket(t *testing.T) {
	pkt1 := &LightAckPacket{
		PktSeqHi: PacketID{Seq: 91},
	}
	pkt1.SetHeader(59, 100)
	testPacket(pkt1, t)
}
