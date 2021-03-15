/*
File Name:  Kademlia.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import "github.com/PeernetOfficial/core/dht"

var nodesDHT *dht.DHT

func initKademlia() {
	nodesDHT = dht.NewDHT(&dht.Node{ID: nodeID}, 256, 20)

	// ShouldEvict determines whether the given node shall be evicted
	// TODO For now always return true.
	nodesDHT.ShouldEvict = func(node *dht.Node) bool {
		return true
	}

	// SendStore sends a store message to the remote node. I.e. asking it to store the given key-value
	nodesDHT.SendStore = func(node *dht.Node, key []byte, value []byte) {
		// TODO
	}

	// SendRequest sends an information request to the remote node. I.e. requesting information.
	nodesDHT.SendRequest = func(request *dht.InformationRequest, nodes []*dht.Node) {
		// TODO
	}
}
