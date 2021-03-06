/*
File Name:  Hash Table.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package dht

import (
	"bytes"
	"errors"
	"math"
	"math/big"
	"math/rand"
	"sort"
	"sync"
	"time"
)

// hashTable represents the hashtable state
type hashTable struct {
	// The ID of the local node
	Self Node

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
	RoutingTable [][]Node // bBits x bSize

	mutex *sync.Mutex
}

func newHashTable(n Node, bits, bucketSize int) *hashTable {
	ht := &hashTable{
		bBits: bits,
		bSize: bucketSize,
		mutex: &sync.Mutex{},
		Self:  n,
	}

	ht.RoutingTable = make([][]Node, ht.bBits)
	return ht
}

func (ht *hashTable) markNodeAsSeen(node []byte) {
	ht.mutex.Lock()
	defer ht.mutex.Unlock()
	index := getBucketIndexFromDifferingBit(ht.bBits, ht.Self.ID, node)
	bucket := ht.RoutingTable[index]
	nodeIndex := -1
	for i, v := range bucket {
		if bytes.Compare(v.ID, node) == 0 {
			nodeIndex = i
			break
		}
	}

	if nodeIndex == -1 {
		panic(errors.New("Tried to mark nonexistent node as seen"))
	}

	n := bucket[nodeIndex]
	n.LastSeen = time.Now().UTC()

	bucket = append(bucket[:nodeIndex], bucket[nodeIndex+1:]...)
	bucket = append(bucket, n)
	ht.RoutingTable[index] = bucket
}

func (ht *hashTable) doesNodeExistInBucket(bucket int, node []byte) bool {
	ht.mutex.Lock()
	defer ht.mutex.Unlock()
	for _, v := range ht.RoutingTable[bucket] {
		if bytes.Compare(v.ID, node) == 0 {
			return true
		}
	}
	return false
}

func (ht *hashTable) getClosestContacts(num int, target []byte, ignoredNodes ...Node) *shortList {
	ht.mutex.Lock()
	defer ht.mutex.Unlock()
	// First we need to build the list of adjacent indices to our target
	// in order
	index := getBucketIndexFromDifferingBit(ht.bBits, ht.Self.ID, target)
	indexList := []int{index}
	for i, j := index-1, index+1; len(indexList) < ht.bBits; i, j = i-1, j+1 {
		if j < ht.bBits {
			indexList = append(indexList, j)
		}
		if i >= 0 {
			indexList = append(indexList, i)
		}
	}

	sl := &shortList{}

	leftToAdd := num

	// Next we select alpha contacts and add them to the short list
	for leftToAdd > 0 && len(indexList) > 0 {
		index, indexList = indexList[0], indexList[1:]
		bucketContacts := len(ht.RoutingTable[index])
		for i := 0; i < bucketContacts; i++ {
			ignored := false
			for j := 0; j < len(ignoredNodes); j++ {
				if bytes.Compare(ht.RoutingTable[index][i].ID, ignoredNodes[j].ID) == 0 {
					ignored = true
				}
			}
			if !ignored {
				sl.AppendUnique(ht.RoutingTable[index][i])
				leftToAdd--
				if leftToAdd == 0 {
					break
				}
			}
		}
	}

	sort.Sort(sl)

	return sl
}

func (ht *hashTable) insertNode(node Node, pinger func(Node) error) {
	index := getBucketIndexFromDifferingBit(ht.bBits, ht.Self.ID, node.ID)

	// Make sure node doesn't already exist
	// If it does, mark it as seen
	if ht.doesNodeExistInBucket(index, node.ID) {
		ht.markNodeAsSeen(node.ID)
		return
	}

	node.LastSeen = time.Now().UTC()

	ht.mutex.Lock()
	defer ht.mutex.Unlock()

	bucket := ht.RoutingTable[index]

	if len(bucket) == ht.bSize {
		if pinger(bucket[0]) != nil {
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

	index := getBucketIndexFromDifferingBit(ht.bBits, ht.Self.ID, ID)
	bucket := ht.RoutingTable[index]

	for i, v := range bucket {
		if bytes.Compare(v.ID, ID) == 0 {
			bucket = append(bucket[:i], bucket[i+1:]...)
		}
	}

	ht.RoutingTable[index] = bucket
}

func (ht *hashTable) getAllNodesInBucketCloserThan(bucket int, id []byte) [][]byte {
	b := ht.RoutingTable[bucket]
	var nodes [][]byte
	for _, v := range b {
		d1 := ht.getDistance(id, ht.Self.ID)
		d2 := ht.getDistance(id, v.ID)

		result := d1.Sub(d1, d2)
		if result.Sign() > -1 {
			nodes = append(nodes, v.ID)
		}
	}

	return nodes
}

func (ht *hashTable) getTotalNodesInBucket(bucket int) int {
	ht.mutex.Lock()
	defer ht.mutex.Unlock()
	return len(ht.RoutingTable[bucket])
}

func (ht *hashTable) getDistance(id1 []byte, id2 []byte) *big.Int {
	dst := make([]byte, ht.bSize)
	for i := 0; i < ht.bSize; i++ {
		dst[i] = id1[i] ^ id2[i]
	}
	ret := big.NewInt(0)
	return ret.SetBytes(dst[:])
}

func (ht *hashTable) getRandomIDFromBucket(bucket int) []byte {
	ht.mutex.Lock()
	defer ht.mutex.Unlock()
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

func (ht *hashTable) lastSeenBefore(cutoff time.Time) (nodes []Node) {
	ht.mutex.Lock()
	defer ht.mutex.Unlock()
	nodes = make([]Node, 0, ht.bSize)
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

func getBucketIndexFromDifferingBit(b int, id1 []byte, id2 []byte) int {
	// Look at each byte from left to right
	for j := 0; j < len(id1); j++ {
		// xor the byte
		xor := id1[j] ^ id2[j]

		// check each bit on the xored result from left to right in order
		for i := 0; i < 8; i++ {
			if hasBit(xor, uint(i)) {
				byteIndex := j * 8
				bitIndex := i
				return b - (byteIndex + bitIndex) - 1
			}
		}
	}

	// the ids must be the same
	// this should only happen during bootstrapping
	return 0
}

func (ht *hashTable) totalNodes() int {
	ht.mutex.Lock()
	defer ht.mutex.Unlock()
	var total int
	for _, v := range ht.RoutingTable {
		total += len(v)
	}
	return total
}

func (ht *hashTable) Nodes() (nodes []Node) {
	ht.mutex.Lock()
	defer ht.mutex.Unlock()
	nodes = make([]Node, 0, ht.bSize)
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
