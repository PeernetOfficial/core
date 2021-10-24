package packet

import (
	"reflect"
	"testing"
)

func testPacket(p Packet, t *testing.T) (read Packet) {
	buf := make([]byte, 1500)
	n, err := p.WriteTo(buf)
	if err != nil {
		t.Errorf("Unable to write packet: %s", err)
	}
	if p2, err := DecodePacket(buf[0:n]); err != nil {
		t.Errorf("Unable to read packet: %s", err)
	} else {
		if !reflect.DeepEqual(p, p2) {
			t.Errorf("Read did not match written.\n\nWrote: %s\nRead:  %s", p, p2)
		}
		read = p2
	}
	return
}
