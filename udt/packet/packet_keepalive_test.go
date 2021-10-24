package packet

import (
	"testing"
)

func TestKeepAlivePacket(t *testing.T) {
	pkt1 := &KeepAlivePacket{}
	pkt1.SetHeader(59, 100)
	testPacket(pkt1, t)
}
