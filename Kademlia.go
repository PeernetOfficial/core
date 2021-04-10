/*
File Name:  Kademlia.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"bytes"
	"time"

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
	nodesDHT.SendRequestStore = func(node *dht.Node, key []byte, dataSize uint64) {
		node.Info.(*PeerInfo).sendAnnouncementStore(key, dataSize)
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

func (peer *PeerInfo) sendAnnouncementStore(fileHash []byte, fileSize uint64) {
	peer.sendAnnouncement(false, false, nil, nil, []InfoStore{{ID: KeyHash{Hash: fileHash}, Size: fileSize, Type: 0}})
}

// ---- CORE DATA FUNCTIONS ----

// Data2Hash returns the hash for the data
func Data2Hash(data []byte) (hash []byte) {
	return hashData(data)
}

// GetData returns the requested data. It checks first the local store and then tries via DHT.
func GetData(hash []byte) (data []byte, found bool) {
	if data, found = GetDataLocal(hash); found {
		return data, found
	}

	return GetDataDHT(hash)
}

// GetDataLocal returns data from the local warehouse.
func GetDataLocal(hash []byte) (data []byte, found bool) {
	return Warehouse.Retrieve(hash)
}

// GetDataDHT requests data via DHT
func GetDataDHT(hash []byte) (data []byte, found bool) {
	data, found, _ = nodesDHT.Get(hash)
	return data, found
}

// StoreDataLocal stores data into the local warehouse.
func StoreDataLocal(data []byte) error {
	key := hashData(data)
	return Warehouse.Store(key, data, time.Time{}, time.Time{})
}

// StoreDataDHT stores data locally and informs peers in the DHT about it.
// Remote peers may choose to keep a record (in case another peers asks) or mirror the full data.
func StoreDataDHT(data []byte) error {
	key := hashData(data)
	if err := Warehouse.Store(key, data, time.Time{}, time.Time{}); err != nil {
		return err
	}
	return nodesDHT.Store(key, uint64(len(data)))
}
