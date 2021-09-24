/*
File Name:  DHT Lite.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

A "lite" DHT implementation without any direct network and store code. There is really no reason for any of the heavy network implementation to be part of this.
*/

package dht

import (
	"encoding/hex"
	"errors"
	"time"
)

// DHT represents the state of the local node in the distributed hash table
type DHT struct {
	ht *hashTable

	// A small number representing the degree of parallelism in network calls.
	// The alpha amount of nodes will be contacted in parallel for finding the target.
	alpha int

	// Functions below must be set and provided by the caller.

	// ShouldEvict determines whether node 1 shall be evicted in favor of node 2
	ShouldEvict func(node1, node2 *Node) bool

	// SendRequestStore sends an announcement-store message to the remote node. It informs the remote node that the local one stores the given key-value.
	SendRequestStore func(node *Node, key []byte, dataSize uint64)

	// SendRequestFindNode sends an information request to find a particular node. nodes are the nodes to send the request to.
	SendRequestFindNode func(request *InformationRequest)

	// SendRequestFindValue sends an information request to find data. nodes are the nodes to send the request to.
	SendRequestFindValue func(request *InformationRequest)

	// FilterSearchStatus is called with updates of searches in the DHT
	FilterSearchStatus func(client *SearchClient, function, format string, v ...interface{})

	// TimeoutSearch is the maximum time a search may take.
	TimeoutSearch time.Duration

	// TimeoutIR is the maximum an information request to a node may take.
	TimeoutIR time.Duration
}

// NewDHT initializes a new DHT node with default values.
func NewDHT(self *Node, bits, bucketSize, alpha int) *DHT {
	return &DHT{
		ht:                 newHashTable(self, bits, bucketSize),
		alpha:              alpha,
		FilterSearchStatus: func(client *SearchClient, function, format string, v ...interface{}) {},
		TimeoutSearch:      10 * time.Second,
		TimeoutIR:          6 * time.Second,
	}
}

// NumNodes returns the total number of nodes stored in the local routing table
func (dht *DHT) NumNodes() int {
	return dht.ht.totalNodes()
}

// Nodes returns the nodes themselves sotred in the routing table.
func (dht *DHT) Nodes() []*Node {
	return dht.ht.Nodes()
}

// GetSelfID returns the identifier of the local node
func (dht *DHT) GetSelfID() []byte {
	return dht.ht.Self.ID
}

// AddNode adds a node into the appropriate k bucket. These buckets are stored in big-endian order so we look at the bits from right to left in order to find the appropriate bucket.
func (dht *DHT) AddNode(node *Node) {
	// The previous code made an immediate ping to the oldest node to "ping the oldest node to find out if it responds back in a reasonable amount of time. If not - remove it."
	// In DHT Lite, however, it will be up to the caller to determine nodes to remove.
	dht.ht.insertNode(node, dht.ShouldEvict)
}

// RemoveNode removes a node
func (dht *DHT) RemoveNode(ID []byte) {
	dht.ht.removeNode(ID)
}

// GetClosestContacts returns the closes contacts in the hash table
func (dht *DHT) GetClosestContacts(count int, target []byte, filterFunc NodeFilterFunc, ignoredNodes ...[]byte) []*Node {
	closest := dht.ht.getClosestContacts(count, target, filterFunc, ignoredNodes...)
	return closest.Nodes
}

// MarkNodeAsSeen marks a node as seen, which pushes it to the top in the bucket list.
func (dht *DHT) MarkNodeAsSeen(ID []byte) {
	dht.ht.markNodeAsSeen(dht.ht.getBucketIndexFromDifferingBit(ID), ID)
}

// IsNodeCloser compares 2 nodes to self. If true, the first node is closer (= smaller distance) to self than the second.
func (dht *DHT) IsNodeCloser(node1, node2 []byte) bool {
	iDist := getDistance(node1, dht.ht.Self.ID)
	jDist := getDistance(node2, dht.ht.Self.ID)

	return iDist.Cmp(jDist) == -1
}

// IsNodeContact checks if the given node is in the local routing table
func (dht *DHT) IsNodeContact(ID []byte) (node *Node) {
	return dht.ht.doesNodeExist(ID)
}

// ---- Synchronous network query functions below ----

// Store informs the network about data stored locally.
// Data size informs how big the data is without sending the actual data. closestCount is the number of closest nodes to contact.
func (dht *DHT) Store(key []byte, dataSize uint64, closestCount int) (err error) {
	if len(key)*8 != dht.ht.bBits {
		return errors.New("invalid key size")
	}

	// TODO: Introduce ActionFindClosestNodes?

	search := dht.NewSearch(ActionFindNode, key, dht.TimeoutSearch, dht.TimeoutIR, dht.alpha)
	search.LogStatus = func(function, format string, v ...interface{}) {
		dht.FilterSearchStatus(search, function, format, v...)
	}
	search.LogStatus("dht.Store", "Search for closest nodes to key %s. Full timeout %s, per node %s. Alpha = %d.\n", hex.EncodeToString(key), dht.TimeoutSearch.String(), dht.TimeoutIR.String(), dht.alpha)
	search.SearchAway()

	// search.Results channel is ignored here. Only the closest nodes to the key are of interest. It is not expected to find a match of key and node ID.
	<-search.TerminateSignal

	// Contact the closes nodes found.
	for n := 0; n < closestCount && n < len(search.list.Nodes); n++ {
		node := search.list.Nodes[n]
		search.LogStatus("dht.Store", "Send info-store message to node %s\n", hex.EncodeToString(node.ID))
		dht.SendRequestStore(node, key, dataSize)
	}

	return nil
}

// Get retrieves data from the network using key
func (dht *DHT) Get(key []byte) (value []byte, senderID []byte, found bool, err error) {
	if len(key)*8 != dht.ht.bBits {
		return nil, nil, false, errors.New("invalid key size")
	}

	search := dht.NewSearch(ActionFindValue, key, dht.TimeoutSearch, dht.TimeoutIR, dht.alpha)
	search.LogStatus = func(function, format string, v ...interface{}) {
		dht.FilterSearchStatus(search, function, format, v...)
	}
	search.LogStatus("dht.Get", "Search for node %s. Full timeout %s, per node %s. Alpha = %d.\n", hex.EncodeToString(key), dht.TimeoutSearch.String(), dht.TimeoutIR.String(), dht.alpha)
	search.SearchAway()

	select {
	case <-search.TerminateSignal:
		return nil, nil, false, nil
	case result := <-search.Results:
		return result.Data, result.SenderID, true, nil
	}
}

// FindNode finds the target node in the network. Blocking!
// The caller may use dht.NewSearch directly and take advantage of the asynchronous response and custom timeouts.
func (dht *DHT) FindNode(key []byte) (node *Node, err error) {
	if len(key)*8 != dht.ht.bBits {
		return nil, errors.New("invalid key size")
	}

	search := dht.NewSearch(ActionFindNode, key, dht.TimeoutSearch, dht.TimeoutIR, dht.alpha)
	search.LogStatus = func(function, format string, v ...interface{}) {
		dht.FilterSearchStatus(search, function, format, v...)
	}
	search.LogStatus("dht.FindNode", "Search for node %s. Full timeout %s, per node %s. Alpha = %d.\n", hex.EncodeToString(key), dht.TimeoutSearch.String(), dht.TimeoutIR.String(), dht.alpha)
	search.SearchAway()

	result, ok := <-search.Results
	if !ok { // Check if closed channel. Redundant with checking <-search.TerminateSignal.
		return nil, nil
	}
	return result.TargetNode, nil
}

// ---- DHT Health ----

// DisableBucketRefresh is an option for debug purposes to reduce noise. It can be useful to disable bucket refresh when debugging outgoing DHT searches.
var DisableBucketRefresh = false

// RefreshBuckets refreshes all buckets not meeting the target node number. 0 to refresh all.
func (dht *DHT) RefreshBuckets(target int) {
	if DisableBucketRefresh {
		return
	}

	for bucket, total := range dht.ht.getTotalNodesPerBucket() {
		if target == 0 || total < target {
			nodeR := dht.ht.getRandomIDFromBucket(bucket)

			// Refreshing closest bucket? Use self ID instead of random one.
			if bucket == 0 {
				nodeR = dht.ht.Self.ID
			}

			dht.FindNode(nodeR)
		}

		if DisableBucketRefresh { // may be disabled while in full refresh which may take some time
			return
		}
	}
}
