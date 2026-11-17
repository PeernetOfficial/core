/*
File Name:  Hash Table.go
Copyright:  2021 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package dht

import (
	"bytes"
	"math/rand"
	"sort"
	"sync"
	"time"
)

// hashTable represents the hashtable state
type hashTable struct {
	// The ID of the local node
	Self *Node

	// the size in bits of the keys used to identify nodes and store and
	// retrieve data; in basic Kademlia this is 160, the length of a SHA1
	bBits int

	// the maximum number of contacts stored in a bucket
	bSize int

	// Routing table a list of all known nodes in the network
	// Nodes within buckets are sorted by least recently seen e.g.
	// [ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ][ ]
	//  ^                                                           ^
	//  └ Least recently seen                    Most recently seen ┘
	RoutingTable [][]*Node // bBits x bSize

	mutex *sync.RWMutex
}

func newHashTable(self *Node, bits, bucketSize int) *hashTable {
	ht := &hashTable{
		bBits: bits,
		bSize: bucketSize,
		mutex: &sync.RWMutex{},
		Self:  self,
	}

	ht.RoutingTable = make([][]*Node, ht.bBits)
	return ht
}

func (ht *hashTable) markNodeAsSeen(index int, ID []byte) {
	ht.mutex.Lock()
	defer ht.mutex.Unlock()
	bucket := ht.RoutingTable[index]
	nodeIndex := -1
	for i, v := range bucket {
		if bytes.Compare(v.ID, ID) == 0 {
			nodeIndex = i
			break
		}
	}

	if nodeIndex == -1 {
		//errors.New("Tried to mark nonexistent node as seen")
		return
	}

	n := bucket[nodeIndex]
	n.LastSeen = time.Now().UTC()

	bucket = append(bucket[:nodeIndex], bucket[nodeIndex+1:]...)
	bucket = append(bucket, n)
	ht.RoutingTable[index] = bucket
}

func (ht *hashTable) doesNodeExistInBucket(bucket int, ID []byte) (node *Node) {
	ht.mutex.RLock()
	defer ht.mutex.RUnlock()
	for _, node = range ht.RoutingTable[bucket] {
		if bytes.Compare(node.ID, ID) == 0 {
			return node
		}
	}
	return nil
}

func (ht *hashTable) doesNodeExist(ID []byte) (node *Node) {
	return ht.doesNodeExistInBucket(ht.getBucketIndexFromDifferingBit(ID), ID)
}

// getClosestContacts returns the num nodes of the routing table closest to the target by XOR distance, closest first.
// filterFunc is optional and allows the caller to filter the nodes. Ignored nodes are left out.
func (ht *hashTable) getClosestContacts(num int, target []byte, filterFunc NodeFilterFunc, ignoredNodes ...[]byte) *shortList {
	ht.mutex.RLock()
	defer ht.mutex.RUnlock()

	sl := newShortList()
	sl.Comparator = target

	if num <= 0 {
		return sl
	}

	// addClosest adds the nodes of the buckets closest to the target first, until the short list holds num nodes.
	addClosest := func(buckets ...[]*Node) {
		group := &shortList{Comparator: target}

		for _, bucket := range buckets {
		bucketLoop:
			for _, node := range bucket {
				for _, ignored := range ignoredNodes {
					if bytes.Equal(node.ID, ignored) {
						continue bucketLoop
					}
				}

				// Use the filter function if set. It allows the caller to only accept certain nodes.
				if filterFunc != nil && !filterFunc(node) {
					continue
				}

				group.Nodes = append(group.Nodes, node)
			}
		}

		sort.Sort(group)

		if left := num - len(sl.Nodes); len(group.Nodes) > left {
			group.Nodes = group.Nodes[:left]
		}
		sl.Nodes = append(sl.Nodes, group.Nodes...)
	}

	// By distance to the target the nodes come in this order: the bucket of the target, then the buckets of a lower index,
	// then the buckets of a higher index in ascending order. The nodes in the buckets of a lower index all differ from the
	// target in the same first bit, they are sorted as one group.
	index := ht.getBucketIndexFromDifferingBit(target)

	addClosest(ht.RoutingTable[index])

	if len(sl.Nodes) < num {
		addClosest(ht.RoutingTable[:index]...)
	}

	for i := index + 1; i < ht.bBits && len(sl.Nodes) < num; i++ {
		addClosest(ht.RoutingTable[i])
	}

	return sl
}

// insertNode adds the node to its bucket. A node that is in the bucket already is marked as seen. A bucket that holds bSize nodes takes no new node.
func (ht *hashTable) insertNode(node *Node) {
	index := ht.getBucketIndexFromDifferingBit(node.ID)

	ht.mutex.Lock()
	defer ht.mutex.Unlock()

	bucket := ht.RoutingTable[index]

	// If the node already exist, mark it as seen
	for i, existing := range bucket {
		if bytes.Equal(existing.ID, node.ID) {
			existing.LastSeen = time.Now().UTC()

			bucket = append(bucket[:i], bucket[i+1:]...)
			ht.RoutingTable[index] = append(bucket, existing)
			return
		}
	}

	if len(bucket) >= ht.bSize {
		return
	}

	node.LastSeen = time.Now().UTC()

	ht.RoutingTable[index] = append(bucket, node)
}

func (ht *hashTable) removeNode(ID []byte) {
	ht.mutex.Lock()
	defer ht.mutex.Unlock()

	index := ht.getBucketIndexFromDifferingBit(ID)
	bucket := ht.RoutingTable[index]

	for i, v := range bucket {
		if bytes.Compare(v.ID, ID) == 0 {
			bucket = append(bucket[:i], bucket[i+1:]...)
		}
	}

	ht.RoutingTable[index] = bucket
}

func (ht *hashTable) getTotalNodesInBucket(bucket int) int {
	ht.mutex.RLock()
	defer ht.mutex.RUnlock()
	return len(ht.RoutingTable[bucket])
}

// getRandomIDFromBucket returns a random ID that falls into the bucket. Up to the bit of the bucket the ID equals the own ID, in that bit it differs, and all later bits are random.
func (ht *hashTable) getRandomIDFromBucket(bucket int) []byte {
	// The first bit in which the ID differs from the own ID, counted from the left. See getBucketIndexFromDifferingBit.
	differingBit := ht.bBits - 1 - bucket
	byteIndex := differingBit / 8

	id := make([]byte, ht.bBits/8)
	for i := range id {
		id[i] = byte(rand.Intn(256))
	}

	copy(id[:byteIndex], ht.Self.ID[:byteIndex])

	// In the byte of the differing bit: the bits before it are taken from the own ID, the bit itself is inverted, the bits after it stay random.
	maskDiffering := byte(0x80) >> (differingBit % 8)
	maskRandom := maskDiffering - 1
	maskSelf := ^(maskDiffering | maskRandom)

	id[byteIndex] = ht.Self.ID[byteIndex]&maskSelf | ^ht.Self.ID[byteIndex]&maskDiffering | id[byteIndex]&maskRandom

	return id
}

func (ht *hashTable) lastSeenBefore(cutoff time.Time) (nodes []*Node) {
	ht.mutex.RLock()
	defer ht.mutex.RUnlock()
	nodes = make([]*Node, 0, ht.bSize)
	for _, v := range ht.RoutingTable {
		for _, n := range v {
			if n.LastSeen.Before(cutoff) {
				nodes = append(nodes, n)
			} else {
				break
			}
		}
	}

	return nodes
}

func (ht *hashTable) getBucketIndexFromDifferingBit(id1 []byte) int {
	// Look at each byte from left to right
	for j := 0; j < len(id1); j++ {
		// xor the byte
		xor := id1[j] ^ ht.Self.ID[j]

		// check each bit on the xored result from left to right in order
		for i := 0; i < 8; i++ {
			if hasBit(xor, uint(i)) {
				byteIndex := j * 8
				bitIndex := i
				return ht.bBits - (byteIndex + bitIndex) - 1
			}
		}
	}

	// the ids must be the same
	// this should only happen during bootstrapping
	return 0
}

func (ht *hashTable) totalNodes() int {
	ht.mutex.RLock()
	defer ht.mutex.RUnlock()
	var total int
	for _, v := range ht.RoutingTable {
		total += len(v)
	}
	return total
}

func (ht *hashTable) Nodes() (nodes []*Node) {
	ht.mutex.RLock()
	defer ht.mutex.RUnlock()
	nodes = make([]*Node, 0, ht.bSize)
	for _, v := range ht.RoutingTable {
		nodes = append(nodes, v...)
	}
	return nodes
}

// Simple helper function to determine the value of a particular bit in a byte by index

// Example:
// number:  1
// bits:    00000001
// pos:     01234567
func hasBit(n byte, pos uint) bool {
	pos = 7 - pos
	val := n & (1 << pos)
	return (val > 0)
}

// getTotalNodesPerBucket returns the count of nodes in all buckets
func (ht *hashTable) getTotalNodesPerBucket() (total []int) {
	ht.mutex.RLock()
	defer ht.mutex.RUnlock()

	for n, _ := range ht.RoutingTable {
		total = append(total, len(ht.RoutingTable[n]))
	}

	return total
}
