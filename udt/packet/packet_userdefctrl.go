package packet

import "errors"

// Structure of packets and functions for writing/reading them

// UserDefControlPacket is a UDT user-defined packet
type UserDefControlPacket struct {
	ctrlHeader
	msgType   uint16 // user-defined message type
	addtlInfo uint32
	data      []byte
}

// WriteTo writes this packet to the provided buffer, returning the length of the packet
func (p *UserDefControlPacket) WriteTo(buf []byte) (uint, error) {
	l := len(buf)
	ol := 16 + len(p.data)
	if l < ol {
		return 0, errors.New("packet too small")
	}

	// Sets the flag bit to indicate this is a control packet
	endianness.PutUint16(buf[0:2], uint16(ptUserDefPkt)|flagBit16)
	endianness.PutUint16(buf[2:4], p.msgType) // Write 16 bit reserved data

	endianness.PutUint32(buf[4:8], p.addtlInfo)
	endianness.PutUint32(buf[8:12], p.ts)
	endianness.PutUint32(buf[12:16], p.DstSockID)

	copy(buf[16:], p.data)

	return uint(ol), nil
}

func (p *UserDefControlPacket) readFrom(data []byte) (err error) {
	if p.addtlInfo, err = p.readHdrFrom(data); err != nil {
		return err
	}
	p.data = data[16:]

	return nil
}

// PacketType returns the packetType associated with this packet
func (p *UserDefControlPacket) PacketType() PacketType {
	return ptUserDefPkt
}
