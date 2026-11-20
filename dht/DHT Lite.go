/*
File Name:  DHT Lite.go
Copyright:  2021 Kleissner Ventures Ltd
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

	// A lookup for a node ends when the beta closest nodes known have all answered.
	beta int

	// A lookup for the nodes that store a blockchain ends when the gamma closest nodes known have all answered.
	gamma int

	// Functions below must be set and provided by the caller.

	// SendRequest sends an information request to request.Nodes. The message to send follows from request.Action.
	SendRequest func(request *InformationRequest)

	// NewNodeAddresses is called when a result of a search for a node reports the node searched for and it is known already.
	// It returns the reported node with the addresses that are not among those of the known nodes, or nil if it has none. The node returned is sent the request.
	NewNodeAddresses func(known []*Node, reported *Node) (node *Node)

	// FilterSearchStatus is called with updates of searches in the DHT
	FilterSearchStatus func(client *SearchClient, function, format string, v ...interface{})

	// TimeoutSearch is the maximum time a search may take.
	TimeoutSearch time.Duration

	// TimeoutIR is the maximum an information request to a node may take.
	TimeoutIR time.Duration
}

// NewDHT initializes a new DHT node with default values.
func NewDHT(self *Node, bits, bucketSize, alpha, beta, gamma int) *DHT {
	return &DHT{
		ht:                 newHashTable(self, bits, bucketSize),
		alpha:              alpha,
		beta:               beta,
		gamma:              gamma,
		NewNodeAddresses:   func(known []*Node, reported *Node) (node *Node) { return nil },
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
// A bucket that holds the bucket size of nodes takes no new node.
func (dht *DHT) AddNode(node *Node) {
	// The previous code made an immediate ping to the oldest node to "ping the oldest node to find out if it responds back in a reasonable amount of time. If not - remove it."
	// In DHT Lite, however, it will be up to the caller to determine nodes to remove.
	dht.ht.insertNode(node)
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

// IsNodeContact checks if the given node is in the local routing table
func (dht *DHT) IsNodeContact(ID []byte) (node *Node) {
	return dht.ht.doesNodeExist(ID)
}

// ---- Synchronous network query functions below ----

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

// FindBlockchain starts a search for the nodes that store the blockchain of the key. Non-blocking.
// Each storing node that a node reports is handed over on the Storing channel of the search; the caller reads it until TerminateSignal is closed.
// The search ends when the gamma closest nodes known have all answered, or when no node is left to ask. The caller may end it earlier with Terminate.
func (dht *DHT) FindBlockchain(key []byte) (search *SearchClient, err error) {
	if len(key)*8 != dht.ht.bBits {
		return nil, errors.New("invalid key size")
	}

	search = dht.NewSearch(ActionFindBlockchain, key, dht.TimeoutSearch, dht.TimeoutIR, dht.alpha)
	search.LogStatus = func(function, format string, v ...interface{}) {
		dht.FilterSearchStatus(search, function, format, v...)
	}
	search.LogStatus("dht.FindBlockchain", "Search for the nodes that store the blockchain %s. Full timeout %s, per node %s. Alpha = %d.\n", hex.EncodeToString(key), dht.TimeoutSearch.String(), dht.TimeoutIR.String(), dht.alpha)
	search.SearchAway()

	return search, nil
}

// ---- DHT Health ----

// DisableBucketRefresh is an option for debug purposes to reduce noise. It can be useful to disable bucket refresh when debugging outgoing DHT searches.
var DisableBucketRefresh = false

// RefreshBuckets fills the routing table. It runs a lookup for the own ID, and a lookup for a random ID in every bucket that holds fewer nodes than the bucket size,
// from the bucket of the farthest nodes to the bucket of the closest node in the routing table. Blocking!
func (dht *DHT) RefreshBuckets() (err error) {
	if DisableBucketRefresh {
		return nil
	}

	if _, err = dht.FindNode(dht.ht.Self.ID); err != nil {
		return err
	}

	total := dht.ht.getTotalNodesPerBucket()

	// The bucket of the closest node is the first one that holds a node.
	closest := 0
	for closest < len(total) && total[closest] == 0 {
		closest++
	}

	for bucket := len(total) - 1; bucket >= closest; bucket-- {
		if DisableBucketRefresh { // may be disabled while in refresh which may take some time
			return nil
		}

		if total[bucket] >= dht.ht.bSize {
			continue
		}

		if _, err = dht.FindNode(dht.ht.getRandomIDFromBucket(bucket)); err != nil {
			return err
		}
	}

	return nil
}
