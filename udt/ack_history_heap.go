package udt

import (
	"sync"
	"time"

	"github.com/PeernetOfficial/core/udt/packet"
)

type ackHistoryEntry struct {
	ackID      uint32
	lastPacket packet.PacketID
	sendTime   time.Time
}

// receiveLossList defines a list of recvLossEntry records
type ackHistoryHeap struct {
	// list contains all entries
	list []ackHistoryEntry

	sync.RWMutex
}

func createHistoryHeap() (heap *ackHistoryHeap) {
	return &ackHistoryHeap{}
}

// Add adds an entry to the list. Deduplication is not performed.
func (heap *ackHistoryHeap) Add(newEntry ackHistoryEntry) {
	heap.Lock()
	defer heap.Unlock()

	heap.list = append(heap.list, newEntry)
}

// Remove removes all IDs matching from the list.
func (heap *ackHistoryHeap) Remove(sequence uint32) (found *ackHistoryEntry) {
	heap.Lock()
	defer heap.Unlock()

	var newList []ackHistoryEntry

	for n := range heap.list {
		if heap.list[n].ackID != sequence {
			newList = append(newList, heap.list[n])
		} else {
			found = &heap.list[n]
		}
	}

	heap.list = newList

	return found
}

// Count returns the number of packets stored
func (heap *ackHistoryHeap) Count() (count int) {
	return len(heap.list)
}
