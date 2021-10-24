package packet

import "errors"

// MessageBoundary flags for where this packet falls within a message
type MessageBoundary uint8

const (
	// MbFirst is the first packet in a multi-packet message
	MbFirst MessageBoundary = 2
	// MbLast is the last packet in a multi-packet message
	MbLast MessageBoundary = 1
	// MbOnly is the only packet in this message
	MbOnly MessageBoundary = 3
	// MbMiddle is neither the first nor last packet in a multi-packet message
	MbMiddle MessageBoundary = 0
)

// DataPacket is a UDT packet containing message data
type DataPacket struct {
	Seq       PacketID // packet sequence number (top bit = 0)
	msg       uint32   // message sequence number (top three bits = message control)
	ts        uint32   // timestamp when message is sent
	DstSockID uint32   // destination socket
	Data      []byte   // payload
}

// PacketType returns the packetType associated with this packet
func (dp *DataPacket) PacketType() PacketType {
	return ptData
}

// SetHeader sets the fields common to UDT data packets
func (dp *DataPacket) SetHeader(destSockID uint32, ts uint32) {
	dp.DstSockID = destSockID
	dp.ts = ts
}

// SocketID sets the Socket ID for this data packet
func (dp *DataPacket) SocketID() (sockID uint32) {
	return dp.DstSockID
}

// SendTime sets the timestamp field for this data packet
func (dp *DataPacket) SendTime() (ts uint32) {
	return dp.ts
}

// SetMessageData sets the message field for this data packet
func (dp *DataPacket) SetMessageData(boundary MessageBoundary, order bool, msg uint32) {
	var iOrder uint32 = 0
	if order {
		iOrder = 0x20000000
	}
	dp.msg = (uint32(boundary) << 30) | iOrder | (msg & 0x1FFFFFFF)
}

// GetMessageData returns the message field for this data packet
func (dp *DataPacket) GetMessageData() (MessageBoundary, bool, uint32) {
	return MessageBoundary(dp.msg >> 30), (dp.msg & 0x20000000) != 0, dp.msg & 0x1FFFFFFF
}

// WriteTo writes this packet to the provided buffer, returning the length of the packet
func (dp *DataPacket) WriteTo(buf []byte) (uint, error) {
	l := len(buf)
	ol := 16 + len(dp.Data)
	if l < ol {
		return 0, errors.New("packet too small")
	}
	endianness.PutUint32(buf[0:4], dp.Seq.Seq&0x7FFFFFFF)
	endianness.PutUint32(buf[4:8], dp.msg)
	endianness.PutUint32(buf[8:12], dp.ts)
	endianness.PutUint32(buf[12:16], dp.DstSockID)
	copy(buf[16:], dp.Data)

	return uint(ol), nil
}

func (dp *DataPacket) readFrom(data []byte) (err error) {
	l := len(data)
	if l < 16 {
		return errors.New("packet too small")
	}
	//dp.seq = endianness.Uint32(data[0:4])
	dp.msg = endianness.Uint32(data[4:8])
	dp.ts = endianness.Uint32(data[8:12])
	dp.DstSockID = endianness.Uint32(data[12:16])

	// The data is whatever is what comes after the 16 bytes of header
	dp.Data = make([]byte, l-16)
	copy(dp.Data, data[16:])

	return
}
