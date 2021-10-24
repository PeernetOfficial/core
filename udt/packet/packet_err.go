package packet

// Structure of packets and functions for writing/reading them

// ErrPacket is a (undocumented) UDT packet describing an out-of-band error code
type ErrPacket struct {
	ctrlHeader
	Errno uint32 // error code
}

// WriteTo writes this packet to the provided buffer, returning the length of the packet
func (p *ErrPacket) WriteTo(buf []byte) (uint, error) {
	return p.writeHdrTo(buf, ptSpecialErr, p.Errno)
}

func (p *ErrPacket) readFrom(data []byte) (err error) {
	p.Errno, err = p.readHdrFrom(data)
	return
}

// PacketType returns the packetType associated with this packet
func (p *ErrPacket) PacketType() PacketType {
	return ptSpecialErr
}
