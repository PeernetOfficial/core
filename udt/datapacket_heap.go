package udt

import (
	"container/heap"

	"github.com/PeernetOfficial/core/udt/packet"
)

// receiveLossList defines a list of recvLossEntry records sorted by their packet ID
type dataPacketHeap []*packet.DataPacket

func (h dataPacketHeap) Len() int {
	return len(h)
}

func (h dataPacketHeap) Less(i, j int) bool {
	return h[i].Seq.Seq < h[j].Seq.Seq
}

func (h dataPacketHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *dataPacketHeap) Push(x interface{}) { // Push and Pop use pointer receivers because they modify the slice's length, not just its contents.
	*h = append(*h, x.(*packet.DataPacket))
}

func (h *dataPacketHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// Find does a binary search of the heap for the specified packetID which is returned
func (h dataPacketHeap) Find(packetID packet.PacketID) (*packet.DataPacket, int) {
	len := len(h)
	idx := 0
	for idx < len {
		pid := h[idx].Seq
		if pid == packetID {
			return h[idx], idx
		} else if pid.Seq > packetID.Seq {
			idx = idx * 2
		} else {
			idx = idx*2 + 1
		}
	}
	return nil, -1
}

// Min does a binary search of the heap for the entry with the lowest packetID greater than or equal to the specified value
func (h dataPacketHeap) Min(greaterEqual packet.PacketID, lessEqual packet.PacketID) (*packet.DataPacket, int) {
	len := len(h)
	idx := 0
	wrapped := greaterEqual.Seq > lessEqual.Seq
	for idx < len {
		pid := h[idx].Seq
		var next int
		if pid.Seq == greaterEqual.Seq {
			return h[idx], idx
		} else if pid.Seq >= greaterEqual.Seq {
			next = idx * 2
		} else {
			next = idx*2 + 1
		}
		if next >= len && h[idx].Seq.Seq > greaterEqual.Seq && (wrapped || h[idx].Seq.Seq <= lessEqual.Seq) {
			return h[idx], idx
		}
		idx = next
	}

	// can't find any packets with greater value, wrap around
	if wrapped {
		idx = 0
		for {
			next := idx * 2
			if next >= len && h[idx].Seq.Seq <= lessEqual.Seq {
				return h[idx], idx
			}
			idx = next
		}
	}
	return nil, -1
}

// Remove does a binary search of the heap for the specified packetID, which is removed
func (h *dataPacketHeap) Remove(packetID packet.PacketID) bool {
	len := len(*h)
	idx := 0
	for idx < len {
		pid := (*h)[idx].Seq
		if pid.Seq == packetID.Seq {
			heap.Remove(h, idx)
			return true
		} else if pid.Seq > packetID.Seq {
			idx = idx * 2
		} else {
			idx = idx*2 + 1
		}
	}
	return false
}
