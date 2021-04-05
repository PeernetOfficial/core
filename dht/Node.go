/*
File Name:  Node.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package dht

import (
	"bytes"
	"math/big"
	"time"
)

// Node is the over-the-wire representation of a node
type Node struct {
	// ID is the unique identifier
	ID []byte

	// LastSeen when was this node last considered seen by the DHT
	LastSeen time.Time

	// Info is an arbitrary pointer specified by the caller
	Info interface{}
}

// shortList is used in order to sort a list of arbitrary nodes against a comparator. These nodes are sorted by xor distance
type shortList struct {
	// Nodes are a list of nodes to be compared
	Nodes []*Node

	// Comparator is the ID to compare to
	Comparator []byte

	// Contacted is a list of nodes that are considered contacted
	Contacted map[string]bool
}

func newShortList() *shortList {
	return &shortList{
		Contacted: make(map[string]bool),
	}
}

func areNodesEqual(n1 *Node, n2 *Node, allowNilID bool) bool {
	if n1 == nil || n2 == nil {
		return false
	}
	if !allowNilID {
		if n1.ID == nil || n2.ID == nil {
			return false
		}
		if bytes.Compare(n1.ID, n2.ID) != 0 {
			return false
		}
	}
	return true
}

func (n *shortList) RemoveNode(ID []byte) {
	for i := 0; i < n.Len(); i++ {
		if bytes.Compare(n.Nodes[i].ID, ID) == 0 {
			n.Nodes = append(n.Nodes[:i], n.Nodes[i+1:]...)
			return
		}
	}
}

func (n *shortList) AppendUniqueNodes(nodes ...*Node) {
nodesLoop:
	for _, vv := range nodes {
		for _, v := range n.Nodes {
			if bytes.Compare(v.ID, vv.ID) == 0 {
				continue nodesLoop
			}
		}
		n.Nodes = append(n.Nodes, vv)
	}
}

func (n *shortList) Len() int {
	return len(n.Nodes)
}

func (n *shortList) Swap(i, j int) {
	n.Nodes[i], n.Nodes[j] = n.Nodes[j], n.Nodes[i]
}

func (n *shortList) Less(i, j int) bool {
	iDist := getDistance(n.Nodes[i].ID, n.Comparator)
	jDist := getDistance(n.Nodes[j].ID, n.Comparator)

	if iDist.Cmp(jDist) == -1 {
		return true
	}

	return false
}

func getDistance(id1 []byte, id2 []byte) *big.Int {
	buf1 := new(big.Int).SetBytes(id1)
	buf2 := new(big.Int).SetBytes(id2)
	result := new(big.Int).Xor(buf1, buf2)
	return result
}

// GetUncontacted returns a list of uncontacted nodes. Each returned node will be marked as contacted.
func (n *shortList) GetUncontacted(count int, useCount bool) (Nodes []*Node) {
	for _, node := range n.Nodes {
		if useCount && count <= 0 {
			break
		}

		// Don't contact nodes already contacted
		if n.Contacted[string(node.ID)] == true {
			continue
		}

		n.Contacted[string(node.ID)] = true
		Nodes = append(Nodes, node)

		count--
	}

	return Nodes
}

// NodeMessage is a message sent by a node
type NodeMessage struct {
	SenderID []byte // Sender of this message
	Data     []byte
	Closest  []*Node
	Error    error
}

// NodeFilterFunc is called to filter nodes based on the callers choice
type NodeFilterFunc func(node *Node) (accept bool)
