package udt

import (
	"sync"
	"time"

	"github.com/PeernetOfficial/core/udt/packet"
)

type recvLossEntry struct {
	packetID packet.PacketID

	// data specific to loss entries
	lastFeedback time.Time
	numNAK       uint
}

// receiveLossList defines a list of recvLossEntry records
type receiveLossHeap struct {
	// list contains all entries
	list []recvLossEntry

	sync.RWMutex
}

func createPacketIDHeap() (heap *receiveLossHeap) {
	return &receiveLossHeap{}
}

// Add adds an entry to the list. Deduplication is not performed.
func (heap *receiveLossHeap) Add(newEntry recvLossEntry) {
	heap.Lock()
	defer heap.Unlock()

	heap.list = append(heap.list, newEntry)
}

// Remove removes all IDs matching from the list.
func (heap *receiveLossHeap) Remove(sequence uint32) (found bool) {
	heap.Lock()
	defer heap.Unlock()

	var newList []recvLossEntry

	for n := range heap.list {
		if heap.list[n].packetID.Seq != sequence {
			newList = append(newList, heap.list[n])
		} else {
			found = true
		}
	}

	heap.list = newList

	return found
}

// Count returns the number of packets stored
func (heap *receiveLossHeap) Count() (count int) {
	return len(heap.list)
}

// Find searches for the packet
func (heap *receiveLossHeap) Find(sequence uint32) (result *recvLossEntry) {
	heap.RLock()
	defer heap.RUnlock()

	for n := range heap.list {
		if heap.list[n].packetID.Seq == sequence {
			return &heap.list[n]
		}
	}

	return nil // not found
}

// Min returns the lowest matching value, if available. Otherwise returns first value.
func (heap *receiveLossHeap) Min(sequenceFrom, sequenceTo uint32) (result *packet.PacketID) {
	heap.RLock()
	defer heap.RUnlock()

	for n := range heap.list {
		if heap.list[n].packetID.Seq >= sequenceFrom && heap.list[n].packetID.Seq < sequenceTo {
			if result == nil || heap.list[n].packetID.Seq < result.Seq {
				result = &heap.list[n].packetID
			}
		}
	}

	return result
}

// RemoveRange removes all packets that are within the given range. Check is from >= and to <.
func (heap *receiveLossHeap) RemoveRange(sequenceFrom, sequenceTo uint32) {
	heap.Lock()
	defer heap.Unlock()

	var newList []recvLossEntry

	for n := range heap.list {
		if !(heap.list[n].packetID.Seq >= sequenceFrom && heap.list[n].packetID.Seq < sequenceTo) {
			newList = append(newList, heap.list[n])
		}
	}

	heap.list = newList
}

// Range returns all packets that are within the given range. Check is from >= and to <.
func (heap *receiveLossHeap) Range(sequenceFrom, sequenceTo uint32) (result []recvLossEntry) {
	heap.RLock()
	defer heap.RUnlock()

	for n := range heap.list {
		if heap.list[n].packetID.Seq >= sequenceFrom && heap.list[n].packetID.Seq < sequenceTo {
			result = append(result, heap.list[n])
		}
	}

	return result
}
