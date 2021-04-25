/*
File Name:  DHT Lite.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

A "lite" DHT implementation without any direct network and store code. There is really no reason for any of the heavy network implementation to be part of this.
*/

package dht

import (
	"bytes"
	"errors"
	"sort"
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

	// The maximum time to wait for a response to any message in Store, Get, FindNode
	TMsgTimeout time.Duration
}

// NewDHT initializes a new DHT node with default values.
func NewDHT(self *Node, bits, bucketSize, alpha int) *DHT {
	return &DHT{
		ht:          newHashTable(self, bits, bucketSize),
		alpha:       alpha,
		TMsgTimeout: 2 * time.Second,
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

// ---- Synchronous network query functions below ----

// Store informs the network about data stored locally.
func (dht *DHT) Store(key []byte, dataSize uint64) (err error) {
	if len(key)*8 != dht.ht.bBits {
		return errors.New("invalid key size")
	}

	// Keep a reference to closestNode. If after performing a search we do not find a closer node, we stop searching.
	sl := dht.ht.getClosestContacts(dht.alpha, key, nil)
	if len(sl.Nodes) == 0 {
		return nil
	}

	closestNode := sl.Nodes[0]

	for {
		info := dht.NewInformationRequest(ActionFindNode, key, sl.GetUncontacted(dht.alpha, true))
		dht.SendRequestFindNode(info)
		results := info.CollectResults(dht.TMsgTimeout)

		for _, result := range results {
			if result.Error != nil {
				sl.RemoveNode(result.SenderID)
				continue
			}
			sl.AppendUniqueNodes(result.Closest...)
		}

		sort.Sort(sl)

		// If closestNode is unchanged then we are done
		if bytes.Equal(sl.Nodes[0].ID, closestNode.ID) {
			for i, node := range sl.Nodes {
				if i >= dht.ht.bSize {
					break
				}

				dht.SendRequestStore(node, key, dataSize)
			}
			return nil
		}

		closestNode = sl.Nodes[0]
	}
}

// Get retrieves data from the network using key
func (dht *DHT) Get(key []byte) (value []byte, senderID []byte, found bool, err error) {
	if len(key)*8 != dht.ht.bBits {
		return nil, nil, false, errors.New("invalid key size")
	}

	// Keep a reference to closestNode. If after performing a search we do not find a closer node, we stop searching.
	sl := dht.ht.getClosestContacts(dht.alpha, key, nil)
	if len(sl.Nodes) == 0 {
		return nil, nil, false, nil
	}

	closestNode := sl.Nodes[0]

	// TODO: Limit max amount of iterations to mitigate malicious responses.
	for {
		info := dht.NewInformationRequest(ActionFindValue, key, sl.GetUncontacted(dht.alpha, true))
		dht.SendRequestFindValue(info)
		results := info.CollectResults(dht.TMsgTimeout)

		for _, result := range results {
			if result.Error != nil {
				sl.RemoveNode(result.SenderID)
				continue
			}
			if len(result.Data) > 0 {
				return result.Data, result.SenderID, true, nil
			}
			sl.AppendUniqueNodes(result.Storing...) // TODO: Assign higher priority, skip closesNode check.
			sl.AppendUniqueNodes(result.Closest...)
		}

		sort.Sort(sl)

		// If closestNode is unchanged then we are done
		if bytes.Equal(sl.Nodes[0].ID, closestNode.ID) {
			return nil, nil, false, nil
		}

		closestNode = sl.Nodes[0]
	}
}

// FindNode finds the target node in the network
func (dht *DHT) FindNode(key []byte) (value []byte, found bool, err error) {
	if len(key)*8 != dht.ht.bBits {
		return nil, false, errors.New("invalid key size")
	}

	// Keep a reference to closestNode. If after performing a search we do not find a closer node, we stop searching.
	sl := dht.ht.getClosestContacts(dht.alpha, key, nil)
	if len(sl.Nodes) == 0 {
		return nil, false, nil
	}

	// According to the Kademlia white paper, after a round of FIND_NODE RPCs fails to provide a node closer than closestNode, we should send a
	// FIND_NODE RPC to all remaining nodes in the shortlist that have not yet been contacted.
	queryRest := false

	closestNode := sl.Nodes[0]

	for {
		info := dht.NewInformationRequest(ActionFindNode, key, sl.GetUncontacted(dht.alpha, !queryRest))
		dht.SendRequestFindNode(info)
		results := info.CollectResults(dht.TMsgTimeout)

		for _, result := range results {
			if result.Error != nil {
				sl.RemoveNode(result.SenderID)
				continue
			}
			sl.AppendUniqueNodes(result.Closest...)

			// TODO: Check if node was found.
		}

		sort.Sort(sl)

		// If closestNode is unchanged then we are done
		if bytes.Equal(sl.Nodes[0].ID, closestNode.ID) || queryRest {
			if !queryRest {
				queryRest = true
				continue
			}
			return nil, false, nil
		}

		closestNode = sl.Nodes[0]
	}
}

// ---- DHT Health ----

// RefreshBuckets refreshes all buckets not meeting the target node number. 0 to refresh all.
func (dht *DHT) RefreshBuckets(target int) {
	for bucket, total := range dht.ht.getTotalNodesPerBucket() {
		if target == 0 || total < target {
			nodeR := dht.ht.getRandomIDFromBucket(bucket)

			// Refreshing closest bucket? Use self ID instead of random one.
			if bucket == 0 {
				nodeR = dht.ht.Self.ID
			}

			dht.FindNode(nodeR)
		}
	}
}
