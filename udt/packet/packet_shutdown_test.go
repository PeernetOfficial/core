package packet

import (
	"testing"
)

func TestShutdownPacket(t *testing.T) {
	pkt1 := &ShutdownPacket{}
	pkt1.SetHeader(59, 100)
	testPacket(pkt1, t)
}
