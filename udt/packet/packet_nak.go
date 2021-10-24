package packet

// Structure of packets and functions for writing/reading them

import (
	"errors"
)

// NakPacket is a UDT packet notifying the peer of lost packets
type NakPacket struct {
	ctrlHeader
	CmpLossInfo []uint32 // integer array of compressed loss information
}

// WriteTo writes this packet to the provided buffer, returning the length of the packet
func (p *NakPacket) WriteTo(buf []byte) (uint, error) {

	off, err := p.writeHdrTo(buf, ptNak, 0)
	if err != nil {
		return 0, err
	}

	l := uint(len(buf))
	if l < off+uint(4*len(p.CmpLossInfo)) {
		return 0, errors.New("packet too small")
	}

	for _, elm := range p.CmpLossInfo {
		endianness.PutUint32(buf[off:off+4], elm)
		off = off + 4
	}

	return off, nil
}

func (p *NakPacket) readFrom(data []byte) error {
	if _, err := p.readHdrFrom(data); err != nil {
		return err
	}
	l := len(data)
	numEntry := (l - 16) / 4
	p.CmpLossInfo = make([]uint32, numEntry)
	for idx := range p.CmpLossInfo {
		st := 16 + 4*idx
		p.CmpLossInfo[idx] = endianness.Uint32(data[st : st+4])
	}
	return nil
}

// PacketType returns the packetType associated with this packet
func (p *NakPacket) PacketType() PacketType {
	return ptNak
}
