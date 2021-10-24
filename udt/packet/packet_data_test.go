package packet

import (
	"testing"
)

func TestDataPacket(t *testing.T) {
	testPacket(
		&DataPacket{
			Seq:       PacketID{Seq: 50},
			ts:        1409,
			DstSockID: 90,
			Data:      []byte("Hello UDT World!"),
		}, t)
}
