package packet

import "math/rand"

// PacketID represents a UDT packet ID sequence
type PacketID struct {
	Seq uint32
}

// Incr increments this packet ID
func (p *PacketID) Incr() {
	p.Seq = (p.Seq + 1) & 0x7FFFFFFF
}

// Decr decrements this packet ID
func (p *PacketID) Decr() {
	p.Seq = (p.Seq - 1) & 0x7FFFFFFF
}

// Add returns a packet ID after adding the specified offset
func (p PacketID) Add(off int32) PacketID {
	return PacketID{uint32((int32(p.Seq) + off)) & 0x7FFFFFFF}
}

// BlindDiff attempts to return the difference after subtracting the argument from itself
func (p PacketID) BlindDiff(rhs PacketID) int32 {
	result := (p.Seq - rhs.Seq) & 0x7FFFFFFF
	if result&0x40000000 != 0 {
		result = result | 0x80000000
	}
	return int32(result)
}

// IsBiggerEqual checks if the current packet sequence is bigger or equal than the parameter
func (p PacketID) IsBiggerEqual(other PacketID) bool {
	return p.BlindDiff(other) >= 0
}

// IsBigger checks if the current packet sequence is bigger than the parameter
func (p PacketID) IsBigger(other PacketID) bool {
	return p.BlindDiff(other) > 0
}

// IsLessEqual checks if the current packet sequence is less or equal than the parameter
func (p PacketID) IsLessEqual(other PacketID) bool {
	return p.BlindDiff(other) <= 0
}

// IsLess checks if the current packet sequence is less than the parameter
func (p PacketID) IsLess(other PacketID) bool {
	return p.BlindDiff(other) < 0
}

// RandomPacketSequence returns a random packet sequence
func RandomPacketSequence() PacketID {
	return PacketID{rand.Uint32() & 0x7FFFFFFF}
}
