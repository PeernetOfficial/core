package udt

import (
	"container/heap"
	"time"

	"github.com/PeernetOfficial/core/udt/packet"
)

type recvLossEntry struct {
	packetID     packet.PacketID
	lastFeedback time.Time
	numNAK       uint
}

// receiveLossList defines a list of recvLossEntry records sorted by their packet ID
type receiveLossHeap []recvLossEntry

func (h receiveLossHeap) Len() int {
	return len(h)
}

func (h receiveLossHeap) Less(i, j int) bool {
	return h[i].packetID.Seq < h[j].packetID.Seq
}

func (h receiveLossHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *receiveLossHeap) Push(x interface{}) { // Push and Pop use pointer receivers because they modify the slice's length, not just its contents.
	*h = append(*h, x.(recvLossEntry))
}

func (h *receiveLossHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// Min does a binary search of the heap for the entry with the lowest packetID greater than or equal to the specified value
func (h receiveLossHeap) Min(greaterEqual packet.PacketID, lessEqual packet.PacketID) (packet.PacketID, int) {
	if len(h) == 0 { // none available!
		return packet.PacketID{Seq: 0}, -1
	}
	return h[0].packetID, 0

	len := len(h)
	idx := 0
	wrapped := greaterEqual.Seq > lessEqual.Seq
	for idx < len {
		pid := h[idx].packetID
		var next int
		if pid.Seq == greaterEqual.Seq {
			return h[idx].packetID, idx
		} else if pid.Seq >= greaterEqual.Seq {
			next = idx * 2
		} else {
			next = idx*2 + 1
		}
		if next >= len && h[idx].packetID.Seq > greaterEqual.Seq && (wrapped || h[idx].packetID.Seq <= lessEqual.Seq) {
			return h[idx].packetID, idx
		}
		idx = next
	}

	// can't find any packets with greater value, wrap around
	if wrapped {
		idx = 0
		for {
			next := idx * 2
			if next >= len && h[idx].packetID.Seq <= lessEqual.Seq {
				return h[idx].packetID, idx
			}
			idx = next
		}
	}
	return packet.PacketID{Seq: 0}, -1
}

// Find does a binary search of the heap for the specified packetID which is returned
func (h receiveLossHeap) Find(packetID packet.PacketID) (*recvLossEntry, int) {
	for n := 0; n < len(h); n++ {
		if h[n].packetID == packetID {
			return &h[n], n
		}
	}

	// len := len(h)
	// idx := 0
	// for idx < len {
	// 	pid := h[idx].packetID
	// 	if pid == packetID {
	// 		return &h[idx], idx
	// 	} else if pid.Seq > packetID.Seq {
	// 		idx = idx * 2
	// 	} else {
	// 		idx = idx*2 + 1
	// 	}
	// }
	return nil, -1
}

// Remove does a binary search of the heap for the specified packetID, which is removed
func (h *receiveLossHeap) Remove(packetID packet.PacketID) bool {
	len := len(*h)
	idx := 0
	for idx < len {
		pid := (*h)[idx].packetID
		if pid == packetID {
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
