package packet

import (
	"testing"
)

func TestCongestionPacket(t *testing.T) {
	pkt1 := &CongestionPacket{}
	pkt1.SetHeader(59, 100)
	testPacket(pkt1, t)
}
