package udt

import (
	"sync"
	"time"

	"github.com/PeernetOfficial/core/udt/packet"
)

type sendPacketEntry struct {
	pkt *packet.DataPacket

	// data specific to sending packets
	tim time.Time
	ttl time.Duration
}

// sendPacketHeap stores a list of packets. Packets are identified by their sequences.
// Access to the list via functions is thread safe.
// This isn't the fastest implementation on the planet since each operation iterates over the entire list. However, it works and is thread safe (the previous code was neither).
type sendPacketHeap struct {
	// list contains all packets
	list []sendPacketEntry

	sync.RWMutex
}

func createPacketHeap() (heap *sendPacketHeap) {
	return &sendPacketHeap{}
}

// Add adds a packet to the list. Deduplication is not performed.
func (heap *sendPacketHeap) Add(newPacket sendPacketEntry) {
	heap.Lock()
	defer heap.Unlock()

	heap.list = append(heap.list, newPacket)
}

// Remove removes all packets with the sequence from the list.
func (heap *sendPacketHeap) Remove(sequence uint32) {
	heap.Lock()
	defer heap.Unlock()

	var newList []sendPacketEntry

	for n := range heap.list {
		if heap.list[n].pkt.Seq.Seq != sequence {
			newList = append(newList, heap.list[n])
		}
	}

	heap.list = newList
}

// Count returns the number of packets stored
func (heap *sendPacketHeap) Count() (count int) {
	return len(heap.list)
}

// Find searches for the packet
func (heap *sendPacketHeap) Find(sequence uint32) (result *sendPacketEntry) {
	heap.RLock()
	defer heap.RUnlock()

	for n := range heap.list {
		if heap.list[n].pkt.Seq.Seq == sequence {
			return &heap.list[n]
		}
	}

	return nil // not found
}

// RemoveRange removes all packets that are within the given range. Check is from >= and to <.
func (heap *sendPacketHeap) RemoveRange(sequenceFrom, sequenceTo packet.PacketID) {
	heap.Lock()
	defer heap.Unlock()

	var newList []sendPacketEntry

	for n := range heap.list {
		if !(heap.list[n].pkt.Seq.IsBiggerEqual(sequenceFrom) && heap.list[n].pkt.Seq.IsLess(sequenceTo)) {
			newList = append(newList, heap.list[n])
		}
	}

	heap.list = newList
}

// Range returns all packets that are within the given range. Check is from >= and to <.
func (heap *sendPacketHeap) Range(sequenceFrom, sequenceTo packet.PacketID) (result []sendPacketEntry) {
	heap.RLock()
	defer heap.RUnlock()

	for n := range heap.list {
		if heap.list[n].pkt.Seq.IsBiggerEqual(sequenceFrom) && heap.list[n].pkt.Seq.IsLess(sequenceTo) {
			result = append(result, heap.list[n])
		}
	}

	return result
}
