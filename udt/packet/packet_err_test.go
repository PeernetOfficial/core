package packet

import (
	"testing"
)

func TestErrPacket(t *testing.T) {
	pkt1 := &ErrPacket{
		Errno: 90,
	}
	pkt1.SetHeader(59, 100)
	testPacket(pkt1, t)
}
