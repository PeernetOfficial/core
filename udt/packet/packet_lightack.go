package packet

// Structure of packets and functions for writing/reading them

import (
	"errors"
)

// LightAckPacket is a UDT variant of the ACK packet for acknowledging received data with minimal information
type LightAckPacket struct {
	ctrlHeader
	PktSeqHi PacketID // The packet sequence number to which all the previous packets have been received (excluding)
}

// WriteTo writes this packet to the provided buffer, returning the length of the packet
func (p *LightAckPacket) WriteTo(buf []byte) (uint, error) {
	l := len(buf)
	if l < 20 {
		return 0, errors.New("packet too small")
	}

	if _, err := p.writeHdrTo(buf, ptAck, 0); err != nil {
		return 0, err
	}

	endianness.PutUint32(buf[16:20], p.PktSeqHi.Seq)

	return 20, nil
}

func (p *LightAckPacket) readFrom(data []byte) (err error) {
	l := len(data)
	if l < 20 {
		return errors.New("packet too small")
	}
	if _, err = p.readHdrFrom(data); err != nil {
		return err
	}
	p.PktSeqHi = PacketID{endianness.Uint32(data[16:20])}

	return nil
}

// PacketType returns the packetType associated with this packet
func (p *LightAckPacket) PacketType() PacketType {
	return ptAck
}
