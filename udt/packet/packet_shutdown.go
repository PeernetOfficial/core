package packet

// Structure of packets and functions for writing/reading them

// ShutdownPacket is a UDT packet notifying the peer of connection shutdown
type ShutdownPacket struct {
	ctrlHeader
}

// WriteTo writes this packet to the provided buffer, returning the length of the packet
func (p *ShutdownPacket) WriteTo(buf []byte) (uint, error) {
	return p.writeHdrTo(buf, ptShutdown, 0)
}

func (p *ShutdownPacket) readFrom(data []byte) (err error) {
	_, err = p.readHdrFrom(data)
	return
}

// PacketType returns the packetType associated with this packet
func (p *ShutdownPacket) PacketType() PacketType {
	return ptShutdown
}
