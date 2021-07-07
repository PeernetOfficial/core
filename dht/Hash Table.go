/*
File Name:  Hash Table.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package dht

import (
	"bytes"
	"math"
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

// getClosestContacts returns the closest nodes to the target. filterFunc is optional and allows the caller to filter the nodes.
func (ht *hashTable) getClosestContacts(num int, target []byte, filterFunc NodeFilterFunc, ignoredNodes ...[]byte) *shortList {
	ht.mutex.RLock()
	defer ht.mutex.RUnlock()

	// First we need to build the list of adjacent indices to our target in order
	index := ht.getBucketIndexFromDifferingBit(target)
	indexList := []int{index}
	for i, j := index-1, index+1; len(indexList) < ht.bBits; i, j = i-1, j+1 {
		if j < ht.bBits {
			indexList = append(indexList, j)
		}
		if i >= 0 {
			indexList = append(indexList, i)
		}
	}

	sl := newShortList()

	leftToAdd := num

	// Next we select alpha contacts and add them to the short list
	for leftToAdd > 0 && len(indexList) > 0 {
		index, indexList = indexList[0], indexList[1:]
		bucketContacts := len(ht.RoutingTable[index])
	bucketLoop:
		for i := 0; i < bucketContacts; i++ {
			for j := 0; j < len(ignoredNodes); j++ {
				if bytes.Compare(ht.RoutingTable[index][i].ID, ignoredNodes[j]) == 0 {
					continue bucketLoop
				}
			}

			// Use the filter function if set. It allows the caller to only accept certain nodes.
			if filterFunc != nil && !filterFunc(ht.RoutingTable[index][i]) {
				continue
			}

			sl.AppendUniqueNodes(ht.RoutingTable[index][i])
			leftToAdd--
			if leftToAdd == 0 {
				break
			}
		}
	}

	sort.Sort(sl)

	return sl
}

func (ht *hashTable) insertNode(node *Node, shouldEvict func(nodeOld *Node, nodeNew *Node) bool) {
	index := ht.getBucketIndexFromDifferingBit(node.ID)

	// If the node already exist, mark it as seen
	if ht.doesNodeExistInBucket(index, node.ID) != nil {
		ht.markNodeAsSeen(index, node.ID)
		return
	}

	node.LastSeen = time.Now().UTC()

	ht.mutex.Lock()
	defer ht.mutex.Unlock()

	bucket := ht.RoutingTable[index]

	if len(bucket) == ht.bSize {
		if shouldEvict(bucket[0], node) {
			bucket = append(bucket, node)
			bucket = bucket[1:]
		}
	} else {
		bucket = append(bucket, node)
	}

	ht.RoutingTable[index] = bucket
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

func (ht *hashTable) getRandomIDFromBucket(bucket int) []byte {
	ht.mutex.RLock()
	defer ht.mutex.RUnlock()
	// Set the new ID to to be equal in every byte up to
	// the byte of the first differing bit in the bucket

	byteIndex := bucket / 8
	var id []byte
	for i := 0; i < byteIndex; i++ {
		id = append(id, ht.Self.ID[i])
	}
	differingBitStart := bucket % 8

	var firstByte byte
	// check each bit from left to right in order
	for i := 0; i < 8; i++ {
		// Set the value of the bit to be the same as the ID
		// up to the differing bit. Then begin randomizing
		var bit bool
		if i < differingBitStart {
			bit = hasBit(ht.Self.ID[byteIndex], uint(i))
		} else {
			bit = rand.Intn(2) == 1
		}

		if bit {
			firstByte += byte(math.Pow(2, float64(7-i)))
		}
	}

	id = append(id, firstByte)

	// Randomize each remaining byte
	for i := byteIndex + 1; i < 20; i++ {
		randomByte := byte(rand.Intn(256))
		id = append(id, randomByte)
	}

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
