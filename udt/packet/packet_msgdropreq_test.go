package packet

import (
	"testing"
)

func TestMsgDropReqPacket(t *testing.T) {
	pkt1 := &MsgDropReqPacket{
		MsgID:    90,
		FirstSeq: PacketID{Seq: 91},
		LastSeq:  PacketID{Seq: 92},
	}
	pkt1.SetHeader(59, 100)
	testPacket(pkt1, t)
}
