package packet

// Structure of packets and functions for writing/reading them

import (
	"errors"
)

// HandshakeReqType describes the type of handshake packet
type HandshakeReqType int32

const (
	// HsRequest represents an attempt to establish a new connection
	HsRequest HandshakeReqType = 1
	//HsRendezvous represents an attempt to establish a new connection using mutual rendezvous packets
	HsRendezvous HandshakeReqType = 0
	//HsResponse is a response to a handshake request
	HsResponse HandshakeReqType = -1
	//HsResponse2 is an acknowledgement that a HsResponse was received
	HsResponse2 HandshakeReqType = -2
	//HsRefused notifies the peer of a connection refusal
	HsRefused HandshakeReqType = 1002
)

// HandshakePacket is a UDT packet used to negotiate a new connection
type HandshakePacket struct {
	ctrlHeader
	UdtVer     uint32     // UDT version
	SockType   SocketType // Socket Type (1 = STREAM or 2 = DGRAM)
	InitPktSeq PacketID   // initial packet sequence number
	//MaxPktSize     uint32           // maximum packet size (including UDP/IP headers)
	MaxFlowWinSize uint32           // maximum flow window size
	ReqType        HandshakeReqType // connection type (regular(1), rendezvous(0), -1/-2 response)
	SockID         uint32           // socket ID
}

// WriteTo writes this packet to the provided buffer, returning the length of the packet
func (p *HandshakePacket) WriteTo(buf []byte) (uint, error) {
	l := len(buf)
	if l < 64 {
		return 0, errors.New("handshake packet too small")
	}

	if _, err := p.writeHdrTo(buf, ptHandshake, 0); err != nil {
		return 0, err
	}

	endianness.PutUint32(buf[16:20], p.UdtVer)
	endianness.PutUint32(buf[20:24], uint32(p.SockType))
	endianness.PutUint32(buf[24:28], p.InitPktSeq.Seq)
	//endianness.PutUint32(buf[28:32], p.MaxPktSize)
	endianness.PutUint32(buf[32:36], p.MaxFlowWinSize)
	endianness.PutUint32(buf[36:40], uint32(p.ReqType))
	endianness.PutUint32(buf[40:44], p.SockID)
	//endianness.PutUint32(buf[44:48], p.SynCookie)

	//sockAddr := make([]byte, 16)
	//copy(sockAddr, p.SockAddr)
	//copy(buf[48:64], sockAddr)

	return 64, nil
}

func (p *HandshakePacket) readFrom(data []byte) error {
	l := len(data)
	if l < 64 {
		return errors.New("handshake packet too small")
	}
	if _, err := p.readHdrFrom(data); err != nil {
		return err
	}
	p.UdtVer = endianness.Uint32(data[16:20])
	p.SockType = SocketType(endianness.Uint32(data[20:24]))
	p.InitPktSeq = PacketID{endianness.Uint32(data[24:28])}
	//p.MaxPktSize = endianness.Uint32(data[28:32])
	p.MaxFlowWinSize = endianness.Uint32(data[32:36])
	p.ReqType = HandshakeReqType(endianness.Uint32(data[36:40]))
	p.SockID = endianness.Uint32(data[40:44])
	//p.SynCookie = endianness.Uint32(data[44:48])

	//p.SockAddr = make(net.IP, 16)
	//copy(p.SockAddr, data[48:64])

	return nil
}

// PacketType returns the packetType associated with this packet
func (p *HandshakePacket) PacketType() PacketType {
	return ptHandshake
}
