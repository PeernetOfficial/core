package udt

import (
	"container/heap"
	"time"

	"github.com/PeernetOfficial/core/udt/packet"
)

type acceptSockInfo struct {
	sockID    uint32
	initSeqNo packet.PacketID
	lastTouch time.Time
	sock      *udtSocket
}

// acceptSockHeap defines a list of acceptSockInfo records sorted by their peer socketID and initial sequence number
type acceptSockHeap []acceptSockInfo

func (h acceptSockHeap) Len() int {
	return len(h)
}

func (h acceptSockHeap) Less(i, j int) bool {
	if h[i].sockID != h[j].sockID {
		return h[i].sockID < h[j].sockID
	}
	return h[i].initSeqNo.Seq < h[j].initSeqNo.Seq
}

func (h acceptSockHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *acceptSockHeap) Push(x interface{}) { // Push and Pop use pointer receivers because they modify the slice's length, not just its contents.
	*h = append(*h, x.(acceptSockInfo))
}

func (h *acceptSockHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

func (h acceptSockHeap) compare(sockID uint32, initSeqNo packet.PacketID, idx int) int {
	if sockID < h[idx].sockID {
		return -1
	}
	if sockID > h[idx].sockID {
		return +1
	}
	if initSeqNo.Seq < h[idx].initSeqNo.Seq {
		return -1
	}
	if initSeqNo.Seq > h[idx].initSeqNo.Seq {
		return +1
	}
	return 0
}

// Find does a binary search of the heap for the specified packetID which is returned
func (h acceptSockHeap) Find(sockID uint32, initSeqNo packet.PacketID) (*udtSocket, int) {
	len := len(h)
	idx := 0
	for idx < len {
		cmp := h.compare(sockID, initSeqNo, idx)
		if cmp == 0 {
			return h[idx].sock, idx
		} else if cmp > 0 {
			idx = idx * 2
		} else {
			idx = idx*2 + 1
		}
	}
	return nil, -1
}

// Prune removes any entries that have a lastTouched before the specified time
func (h *acceptSockHeap) Prune(pruneBefore time.Time) {
	for {
		l := len(*h)
		foundOne := false
		for idx := 0; idx < l; idx++ {
			if (*h)[idx].lastTouch.Before(pruneBefore) {
				foundOne = true
				heap.Remove(h, idx)
				break
			}
		}
		if !foundOne {
			// nothing left to prune
			return
		}
	}
}
