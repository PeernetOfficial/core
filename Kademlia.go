/*
File Name:  Kademlia.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"bytes"
	"crypto/ed25519"
	"time"

	"github.com/PeernetOfficial/core/dht"
	"github.com/PeernetOfficial/core/protocol"
)

const alpha = 5       // Count of nodes to be contacted in parallel for finding a key
const bucketSize = 20 // Count of nodes per bucket

func (backend *Backend) initKademlia() {
	backend.nodesDHT = dht.NewDHT(&dht.Node{ID: backend.peerPublicKey}, 256, bucketSize, alpha)

	// ShouldEvict determines whether node 1 shall be evicted in favor of node 2
	backend.nodesDHT.ShouldEvict = func(node1, node2 *dht.Node) bool {
		rttOld := node1.Info.(*PeerInfo).GetRTT()
		rttNew := node2.Info.(*PeerInfo).GetRTT()

		// evict the old node if the new one has a faster ping time
		if rttOld == 0 { // old one has no recent RTT (happens if all connections are inactive)?
			return true
		} else if rttNew > 0 {
			// If new RTT is smaller, evict old one.
			return rttNew < rttOld
		}

		// If here, none has a RTT. Keep the closer (by distance) one.
		return backend.nodesDHT.IsNodeCloser(node1.ID, node2.ID)
	}

	// SendRequestStore sends a store message to the remote node. I.e. asking it to store the given key-value
	backend.nodesDHT.SendRequestStore = func(node *dht.Node, key []byte, dataSize uint64) {
		node.Info.(*PeerInfo).sendAnnouncementStore(key, dataSize)
	}

	// SendRequestFindNode sends an information request to find a particular node. nodes are the nodes to send the request to.
	backend.nodesDHT.SendRequestFindNode = func(request *dht.InformationRequest) {
		for _, node := range request.Nodes {
			node.Info.(*PeerInfo).sendAnnouncementFindNode(request)
		}
	}

	// SendRequestFindValue sends an information request to find data. nodes are the nodes to send the request to.
	backend.nodesDHT.SendRequestFindValue = func(request *dht.InformationRequest) {
		for _, node := range request.Nodes {
			node.Info.(*PeerInfo).sendAnnouncementFindValue(request)
		}
	}

	backend.nodesDHT.FilterSearchStatus = backend.Filters.DHTSearchStatus
}

// autoBucketRefresh refreshes buckets every 5 minutes to meet the alpha nodes per bucket target. Force full refresh every hour.
func (backend *Backend) autoBucketRefresh() {
	for minute := 5; ; minute += 5 {
		time.Sleep(time.Minute * 5)

		target := alpha
		if minute%60 == 0 {
			target = 0
		}

		backend.nodesDHT.RefreshBuckets(target)
	}
}

// bootstrapKademlia bootstraps the Kademlia bucket list
func (backend *Backend) bootstrapKademlia() {
	monitor := make(chan *PeerInfo)
	backend.registerPeerMonitor(monitor)

	// Wait until there are at least 2 peers connected.
	for {
		<-monitor
		if backend.nodesDHT.NumNodes() >= 2 {
			break
		}
	}

	backend.unregisterPeerMonitor(monitor)

	// Refresh every 10 seconds 3 times
	for n := 0; n < 3; n++ {
		backend.nodesDHT.RefreshBuckets(alpha)

		time.Sleep(time.Second)
	}
}

// Future sendAnnouncementX: If it detects that announcements are sent out to the same peer within 50ms it should activate a wait-and-group scheme.

// sendAnnouncementFindNode sends A_FIND_PEER for the requested key, or A_FIND_SELF if the key is self.
func (peer *PeerInfo) sendAnnouncementFindNode(request *dht.InformationRequest) {
	if bytes.Equal(request.Key, peer.Backend.peerPublicKey) {
		peer.sendAnnouncement(false, &protocol.Requests{FindSelf: true}, request)
	} else {
		peer.sendAnnouncement(false, &protocol.Requests{FindPeer: [][]byte{request.Key}}, request)
	}
}

// sendAnnouncementFindValue sends B_FIND_FILE for the requested key.
func (peer *PeerInfo) sendAnnouncementFindValue(request *dht.InformationRequest) {
	peer.sendAnnouncement(false, &protocol.Requests{FindFile: [][]byte{request.Key}}, request)
}

// sendAnnouncementStore sends B_STORE_FILE for the file.
func (peer *PeerInfo) sendAnnouncementStore(fileHash []byte, fileSize uint64) {
	peer.sendAnnouncement(false, &protocol.Requests{StoreFile: []protocol.FileStore{{Hash: fileHash, Size: fileSize, Type: 0}}}, nil)
}

// ---- CORE DATA FUNCTIONS ----

// Data2Hash returns the hash for the data
func Data2Hash(data []byte) (hash []byte) {
	return protocol.HashData(data)
}

// GetData returns the requested data. It checks first the local store and then tries via DHT. The sender is the peer ID of the peer that provided the data.
func (backend *Backend) GetData(hash []byte) (data []byte, senderPublicKey []byte, found bool) {
	if data, found = backend.GetDataLocal(hash); found {
		return data, backend.peerPublicKey, found
	}

	return backend.GetDataDHT(hash)
}

// GetDataLocal returns data from the local warehouse.
func (backend *Backend) GetDataLocal(hash []byte) (data []byte, found bool) {
	return backend.dhtStore.Get(hash)
}

// GetDataDHT requests data via DHT. The sender is the peer ID of the peer that provided the data.
func (backend *Backend) GetDataDHT(hash []byte) (data []byte, senderPublicKey []byte, found bool) {
	data, senderPublicKey, found, _ = backend.nodesDHT.Get(hash)
	return data, senderPublicKey, found
}

// StoreDataLocal stores data into the local warehouse.
func (backend *Backend) StoreDataLocal(data []byte) error {
	key := protocol.HashData(data)
	return backend.dhtStore.Set(key, data)
}

// StoreDataDHT stores data locally and informs closestCount peers in the DHT about it.
// Remote peers may choose to keep a record (in case another peers asks) or mirror the full data.
func (backend *Backend) StoreDataDHT(data []byte, closestCount int) error {
	key := protocol.HashData(data)
	if err := backend.dhtStore.Set(key, data); err != nil {
		return err
	}
	return backend.nodesDHT.Store(key, uint64(len(data)), closestCount)
}

// ---- NODE FUNCTIONS ----

// IsNodeContact checks if the peer is a contact in the local DHT routing table
func (backend *Backend) IsNodeContact(publicKey ed25519.PublicKey) (node *dht.Node, peer *PeerInfo) {
	node = backend.nodesDHT.IsNodeContact(publicKey)
	if node == nil {
		return nil, nil
	}

	return node, node.Info.(*PeerInfo)
}

// FindNode finds a peer via the DHT
func (backend *Backend) FindNode(publicKey ed25519.PublicKey, Timeout time.Duration) (node *dht.Node, peer *PeerInfo, err error) {
	// first check if in the peer list
	if peer = backend.PeerlistLookup(publicKey); peer != nil {
		return nil, peer, nil
	}

	// Search the peer via DHT.
	node, err = backend.nodesDHT.FindNode(publicKey)
	if node == nil {
		return nil, nil, err
	}

	return node, node.Info.(*PeerInfo), err
}

// ---- Asynchronous Search ----

// AsyncSearch creates an async search for the given key in the DHT.
// Timeout is the total time the search may take, covering all information requests. TimeoutIR is the time an information request may take.
// Alpha is the number of concurrent requests that will be performed.
func (backend *Backend) AsyncSearch(Action int, Key []byte, Timeout, TimeoutIR time.Duration, Alpha int) (client *dht.SearchClient) {
	return backend.nodesDHT.NewSearch(Action, Key, Timeout, TimeoutIR, Alpha)
}
