package packet

// Structure of packets and functions for writing/reading them

import (
	"errors"
)

// AckPacket is a UDT packet acknowledging previously-received data packets and describing the state of the link
type AckPacket struct {
	ctrlHeader
	AckSeqNo  uint32   // ACK sequence number
	PktSeqHi  PacketID // The packet sequence number to which all the previous packets have been received (excluding)
	Rtt       uint32   // RTT (in microseconds)
	RttVar    uint32   // RTT variance
	BuffAvail uint32   // Available buffer size (in bytes)

	// the following data is optional (not sent more than SYN)
	IncludeLink bool
	PktRecvRate uint32 // Packets receiving rate (in number of packets per second)
	EstLinkCap  uint32 // Estimated link capacity (in number of packets per second)
}

// WriteTo writes this packet to the provided buffer, returning the length of the packet
func (p *AckPacket) WriteTo(buf []byte) (uint, error) {
	l := len(buf)
	if l < 32 {
		return 0, errors.New("packet too small")
	}

	if _, err := p.writeHdrTo(buf, ptAck, p.AckSeqNo); err != nil {
		return 0, err
	}

	endianness.PutUint32(buf[16:20], p.PktSeqHi.Seq)
	endianness.PutUint32(buf[20:24], p.Rtt)
	endianness.PutUint32(buf[24:28], p.RttVar)
	endianness.PutUint32(buf[28:32], p.BuffAvail)
	if p.IncludeLink {
		if l < 40 {
			return 0, errors.New("packet too small")
		}
		endianness.PutUint32(buf[32:36], p.PktRecvRate)
		endianness.PutUint32(buf[36:40], p.EstLinkCap)
		return 40, nil
	}

	return 32, nil
}

func (p *AckPacket) readFrom(data []byte) (err error) {
	l := len(data)
	if l < 32 {
		return errors.New("packet too small")
	}
	if p.AckSeqNo, err = p.readHdrFrom(data); err != nil {
		return err
	}
	p.PktSeqHi = PacketID{endianness.Uint32(data[16:20])}
	p.Rtt = endianness.Uint32(data[20:24])
	p.RttVar = endianness.Uint32(data[24:28])
	p.BuffAvail = endianness.Uint32(data[28:32])
	if l >= 36 {
		p.IncludeLink = true
		p.PktRecvRate = endianness.Uint32(data[32:36])
		if l >= 40 {
			p.EstLinkCap = endianness.Uint32(data[36:40])
		}
	}

	return nil
}

// PacketType returns the packetType associated with this packet
func (p *AckPacket) PacketType() PacketType {
	return ptAck
}
