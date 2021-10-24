package packet

// Structure of packets and functions for writing/reading them

// KeepAlivePacket is a UDT packet used to keep a connection alive when no data is being sent
type KeepAlivePacket struct {
	ctrlHeader
}

// WriteTo writes this packet to the provided buffer, returning the length of the packet
func (p *KeepAlivePacket) WriteTo(buf []byte) (uint, error) {
	return p.writeHdrTo(buf, ptKeepalive, 0)
}

func (p *KeepAlivePacket) readFrom(data []byte) (err error) {
	_, err = p.readHdrFrom(data)
	return
}

// PacketType returns the packetType associated with this packet
func (p *KeepAlivePacket) PacketType() PacketType {
	return ptKeepalive
}
