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

// IterateX are actions on the DHT
const (
	IterateStore     = iota // Store information in the network
	IterateFindNode         // Find a node
	IterateFindValue        // Find a value
)

// DHT represents the state of the local node in the distributed hash table
type DHT struct {
	ht *hashTable

	// A small number representing the degree of parallelism in network calls.
	// The alpha amount of nodes will be contacted in parallel for finding the target.
	alpha int

	// Functions below must be set and provided by the caller.

	// ShouldEvict determines whether the given node shall be evicted
	ShouldEvict func(node *Node) bool

	// SendStore sends a store message to the remote node. I.e. asking it to store the given key-value.
	SendStore func(node *Node, key []byte, value []byte)

	// SendRequest sends an information request to the remote node. I.e. requesting information.
	// The returned results channel will be closed when no more results are to be expected.
	SendRequest func(request *InformationRequest, nodes []*Node)

	// The maximum time to wait for a response to any message
	TMsgTimeout time.Duration
}

// NewDHT initializes a new DHT node with default values.
func NewDHT(self *Node, bits, bucketSize int) *DHT {
	return &DHT{
		ht:          newHashTable(self, bits, bucketSize),
		alpha:       3,
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

// Store stores data on the network. This will trigger an IterateStore message.
func (dht *DHT) Store(key, data []byte) (err error) {
	_, _, err = dht.iterate(IterateStore, key[:], data)
	return err
}

// Get retrieves data from the network using key
func (dht *DHT) Get(key []byte) (value []byte, found bool, err error) {
	value, _, err = dht.iterate(IterateFindValue, key, nil)
	return value, value != nil, err
}

// FindNode finds the target node in the network
func (dht *DHT) FindNode(key []byte) (value []byte, found bool, err error) {
	value, _, err = dht.iterate(IterateFindNode, key, nil)
	return value, value != nil, err
}

// Iterate does an iterative search through the network. This can be done
// for multiple reasons. These reasons include:
//     IterateStore - Used to store new information in the network.
//     IterateFindNode - Used to bootstrap the network.
//     IterateFindValue - Used to find a value among the network given a key.
func (dht *DHT) iterate(action int, target []byte, data []byte) (value []byte, closest []*Node, err error) {
	if len(target) != dht.ht.bBits {
		return nil, nil, errors.New("invalid key")
	} else if action < IterateStore || action > IterateFindValue {
		return nil, nil, errors.New("unknown iterate type")
	}

	sl := dht.ht.getClosestContacts(dht.alpha, target, nil)

	// We keep a reference to the closestNode. If after performing a search we do not find a closer node, we stop searching.
	if len(sl.Nodes) == 0 {
		return nil, nil, nil
	}

	// According to the Kademlia white paper, after a round of FIND_NODE RPCs fails to provide a node closer than closestNode, we should send a
	// FIND_NODE RPC to all remaining nodes in the shortlist that have not yet been contacted.
	queryRest := false

	closestNode := sl.Nodes[0]

	for {
		info := NewInformationRequest(action, target)
		dht.SendRequest(info, sl.GetUncontacted(dht.alpha, !queryRest))
		results := info.CollectResults(dht.TMsgTimeout)

		for _, result := range results {
			if result.Error != nil {
				sl.RemoveNode(result.SenderID)
				continue
			}
			switch action {
			case IterateFindNode:
				sl.AppendUniqueNodes(result.Closest...)
				// TODO: Accept contact info?
			case IterateFindValue:
				// When an IterateFindValue succeeds, the initiator COULD store the key/value pair at the closest node seen which did not return the value.
				if len(result.Data) > 0 {
					return result.Data, nil, nil
				}
				sl.AppendUniqueNodes(result.Closest...)
			case IterateStore:
				sl.AppendUniqueNodes(result.Closest...)
			}
		}

		sort.Sort(sl)

		// If closestNode is unchanged then we are done
		if bytes.Compare(sl.Nodes[0].ID, closestNode.ID) == 0 || queryRest {
			// We are done
			switch action {
			case IterateFindNode:
				if !queryRest {
					queryRest = true
					continue
				}
				return nil, sl.Nodes, nil

			case IterateFindValue:
				return nil, sl.Nodes, nil

			case IterateStore:
				for i, node := range sl.Nodes {
					if i >= dht.ht.bSize {
						break
					}

					dht.SendStore(node, target, data)
				}
				return nil, nil, nil
			}
		}

		closestNode = sl.Nodes[0]
	}
}
