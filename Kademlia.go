/*
File Name:  Kademlia.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"bytes"

	"github.com/PeernetOfficial/core/dht"
)

var nodesDHT *dht.DHT

func initKademlia() {
	nodesDHT = dht.NewDHT(&dht.Node{ID: nodeID}, 256, 20, 5)

	// ShouldEvict determines whether the given node shall be evicted
	nodesDHT.ShouldEvict = func(node *dht.Node) bool {
		// TODO: logic
		return true
	}

	// SendRequestStore sends a store message to the remote node. I.e. asking it to store the given key-value
	nodesDHT.SendRequestStore = func(node *dht.Node, key []byte, value []byte) {
		node.Info.(*PeerInfo).sendAnnouncementStore(key, value)
	}

	// SendRequestFindNode sends an information request to find a particular node. nodes are the nodes to send the request to.
	nodesDHT.SendRequestFindNode = func(request *dht.InformationRequest) {
		for _, node := range request.Nodes {
			node.Info.(*PeerInfo).sendAnnouncementFindNode(request)
		}
	}

	// SendRequestFindValue sends an information request to find data. nodes are the nodes to send the request to.
	nodesDHT.SendRequestFindValue = func(request *dht.InformationRequest) {
		for _, node := range request.Nodes {
			node.Info.(*PeerInfo).sendAnnouncementFindValue(request)
		}
	}
}

// Future sendAnnouncementX: If it detects that announcements are sent out to the same peer within 50ms it should activate a wait-and-group scheme.

func (peer *PeerInfo) sendAnnouncementFindNode(request *dht.InformationRequest) {
	// If the key is self, send it as FIND_SELF
	if bytes.Equal(request.Key, nodeID) {
		peer.sendAnnouncement(false, true, nil, nil, nil)
	} else {
		peer.sendAnnouncement(false, false, []KeyHash{{Hash: request.Key}}, nil, nil)
	}
}

func (peer *PeerInfo) sendAnnouncementFindValue(request *dht.InformationRequest) {

	findSelf := false
	var findPeer []KeyHash
	var findValue []KeyHash

	findValue = append(findValue, KeyHash{Hash: request.Key})

	peer.sendAnnouncement(false, findSelf, findPeer, findValue, nil)
}

func (peer *PeerInfo) sendAnnouncementStore(key []byte, value []byte) {
	peer.sendAnnouncement(false, false, nil, nil, []InfoStore{{ID: KeyHash{Hash: key}, Size: uint64(len(value)), Type: 0}})
}
