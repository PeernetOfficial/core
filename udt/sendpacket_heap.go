package udt

import (
	"container/heap"
	"time"

	"github.com/PeernetOfficial/core/udt/packet"
)

type sendPacketEntry struct {
	pkt *packet.DataPacket
	tim time.Time
	ttl time.Duration
}

// receiveLossList defines a list of recvLossEntry records sorted by their packet ID
type sendPacketHeap []sendPacketEntry

func (h sendPacketHeap) Len() int {
	return len(h)
}

func (h sendPacketHeap) Less(i, j int) bool {
	return h[i].pkt.Seq.Seq < h[j].pkt.Seq.Seq
}

func (h sendPacketHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *sendPacketHeap) Push(x interface{}) { // Push and Pop use pointer receivers because they modify the slice's length, not just its contents.
	*h = append(*h, x.(sendPacketEntry))
}

func (h *sendPacketHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// Find does a binary search of the heap for the specified packetID which is returned
func (h sendPacketHeap) Find(packetID packet.PacketID) (*sendPacketEntry, int) {
	len := len(h)
	idx := 0
	for idx < len {
		pid := h[idx].pkt.Seq
		if pid == packetID {
			return &h[idx], idx
		} else if pid.Seq > packetID.Seq {
			idx = idx * 2
		} else {
			idx = idx*2 + 1
		}
	}
	return nil, -1
}

// Min does a binary search of the heap for the entry with the lowest packetID greater than or equal to the specified value
func (h sendPacketHeap) Min(greaterEqual packet.PacketID, lessEqual packet.PacketID) (*packet.DataPacket, int) {
	len := len(h)
	idx := 0
	wrapped := greaterEqual.Seq > lessEqual.Seq
	for idx < len {
		pid := h[idx].pkt.Seq
		var next int
		if pid.Seq == greaterEqual.Seq {
			return h[idx].pkt, idx
		} else if pid.Seq >= greaterEqual.Seq {
			next = idx * 2
		} else {
			next = idx*2 + 1
		}
		if next >= len && h[idx].pkt.Seq.Seq > greaterEqual.Seq && (wrapped || h[idx].pkt.Seq.Seq <= lessEqual.Seq) {
			return h[idx].pkt, idx
		}
		idx = next
	}

	// can't find any packets with greater value, wrap around
	if wrapped {
		idx = 0
		for {
			next := idx * 2
			if next >= len && h[idx].pkt.Seq.Seq <= lessEqual.Seq {
				return h[idx].pkt, idx
			}
			idx = next
		}
	}
	return nil, -1
}

// Remove does a binary search of the heap for the specified packetID, which is removed
func (h *sendPacketHeap) Remove(packetID packet.PacketID) bool {
	len := len(*h)
	idx := 0
	for idx < len {
		pid := (*h)[idx].pkt.Seq
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
