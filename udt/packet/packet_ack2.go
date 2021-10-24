package packet

// Structure of packets and functions for writing/reading them

// Ack2Packet is a UDT packet acknowledging receipt of an ACK packet
type Ack2Packet struct {
	ctrlHeader
	AckSeqNo uint32 // ACK sequence number
}

// WriteTo writes this packet to the provided buffer, returning the length of the packet
func (p *Ack2Packet) WriteTo(buf []byte) (uint, error) {
	return p.writeHdrTo(buf, ptAck2, p.AckSeqNo)
}

func (p *Ack2Packet) readFrom(data []byte) (err error) {
	p.AckSeqNo, err = p.readHdrFrom(data)
	return
}

// PacketType returns the packetType associated with this packet
func (p *Ack2Packet) PacketType() PacketType {
	return ptAck2
}
