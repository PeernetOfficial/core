package udt

import (
	"sync"
	"time"

	"github.com/PeernetOfficial/core/udt/packet"
)

type recvLossEntry struct {
	packetID packet.PacketID

	// data specific to loss entries
	lastResend     time.Time // When the lost packet was last resent
	attemptsResend uint      // How many times this packet was sent out
	numNAK         uint
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

	if found {
		heap.list = newList
	}

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

// RemoveRange removes all packets that are within the given range. Check is from >= and to <.
func (heap *receiveLossHeap) RemoveRange(sequenceFrom, sequenceTo packet.PacketID) {
	heap.Lock()
	defer heap.Unlock()

	var newList []recvLossEntry

	for n := range heap.list {
		if !(heap.list[n].packetID.IsBiggerEqual(sequenceFrom) && heap.list[n].packetID.IsLess(sequenceTo)) {
			newList = append(newList, heap.list[n])
		}
	}

	heap.list = newList
}

// Range returns all packets that are within the given range as pointers. Check is from >= and to <.
func (heap *receiveLossHeap) Range(sequenceFrom, sequenceTo packet.PacketID) (result []*recvLossEntry) {
	heap.RLock()
	defer heap.RUnlock()

	for n := range heap.list {
		if heap.list[n].packetID.IsBiggerEqual(sequenceFrom) && heap.list[n].packetID.IsLess(sequenceTo) {
			result = append(result, &heap.list[n])
		}
	}

	return result
}
