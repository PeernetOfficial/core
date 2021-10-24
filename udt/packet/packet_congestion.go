package packet

// Structure of packets and functions for writing/reading them

// CongestionPacket is a (deprecated) UDT packet notifying the peer of increased congestion
type CongestionPacket struct {
	ctrlHeader
}

// WriteTo writes this packet to the provided buffer, returning the length of the packet
func (p *CongestionPacket) WriteTo(buf []byte) (uint, error) {
	return p.writeHdrTo(buf, ptCongestion, 0)
}

func (p *CongestionPacket) readFrom(data []byte) (err error) {
	_, err = p.readHdrFrom(data)
	return
}

// PacketType returns the packetType associated with this packet
func (p *CongestionPacket) PacketType() PacketType {
	return ptCongestion
}
