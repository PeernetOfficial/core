package udt

import (
	"time"

	"github.com/PeernetOfficial/core/udt/packet"
)

type ackHistoryEntry struct {
	ackID      uint32
	lastPacket packet.PacketID
	sendTime   time.Time
}

// receiveLossList defines a list of ACK records sorted by their ACK id
type ackHistoryHeap []*ackHistoryEntry

func (h ackHistoryHeap) Len() int {
	return len(h)
}

func (h ackHistoryHeap) Less(i, j int) bool {
	return h[i].ackID < h[j].ackID
}

func (h ackHistoryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *ackHistoryHeap) Push(x interface{}) { // Push and Pop use pointer receivers because they modify the slice's length, not just its contents.
	*h = append(*h, x.(*ackHistoryEntry))
}

func (h *ackHistoryHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// Find does a binary search of the heap for the specified ackID which is returned
func (h ackHistoryHeap) Find(ackID uint32) (*ackHistoryEntry, int) {
	for n := 0; n < len(h); n++ {
		if h[n].ackID == ackID {
			return h[n], n
		}
	}

	// len := len(h)
	// idx := 0
	// for idx < len {
	// 	here := h[idx].ackID
	// 	if here == ackID {
	// 		return h[idx], idx
	// 	} else if here > ackID {
	// 		idx = idx * 2
	// 	} else {
	// 		idx = idx*2 + 1
	// 	}
	// }
	return nil, -1
}
