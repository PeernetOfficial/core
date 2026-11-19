/*
File Name:  Kademlia.go
Copyright:  2021 Kleissner Ventures Ltd
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

const alpha = 5       // Count of nodes to be contacted in parallel for finding a key (α in the whitepaper)
const beta = 5        // Count of closest nodes that must have answered for a lookup to end (β in the whitepaper)
const bucketSize = 20 // Count of nodes per bucket (k in the whitepaper)

// bucketRefreshTime is the interval of the routing table refresh.
const bucketRefreshTime = 5 * time.Minute

func (backend *Backend) initKademlia() {
	backend.nodesDHT = dht.NewDHT(&dht.Node{ID: backend.peerPublicKey}, 256, bucketSize, alpha, beta)

	// SendRequest sends the announcement matching the action of the information request to the nodes.
	backend.nodesDHT.SendRequest = func(request *dht.InformationRequest) {
		for _, node := range request.Nodes {
			peer := node.Info.(*PeerInfo)

			switch request.Action {
			case dht.ActionFindNode:
				peer.sendAnnouncementFindPeer(request)
			}
		}
	}

	backend.nodesDHT.NewNodeAddresses = newNodeAddresses

	backend.nodesDHT.FilterSearchStatus = backend.Filters.DHTSearchStatus
}

// newNodeAddresses is called when a response reports the peer searched for and it is known already. It returns the reported peer with the addresses that are not among those of the known ones,
// or nil if it has none. A peer of the peer list is connected, no further address is tried. Two addresses are equal if the IP and the ports are equal.
func newNodeAddresses(known []*dht.Node, reported *dht.Node) (node *dht.Node) {
	peer := reported.Info.(*PeerInfo)
	if !peer.isVirtual {
		return nil
	}

	var addresses []*peerAddress

addressLoop:
	for _, address := range peer.targetAddresses {
		for _, knownNode := range known {
			knownPeer := knownNode.Info.(*PeerInfo)
			if !knownPeer.isVirtual {
				return nil
			}

			for _, tried := range knownPeer.targetAddresses {
				if tried.IP.Equal(address.IP) && tried.Port == address.Port && tried.PortInternal == address.PortInternal && tried.PortExternal == address.PortExternal {
					continue addressLoop
				}
			}
		}

		addresses = append(addresses, address)
	}

	if len(addresses) == 0 {
		return nil
	}

	peer.targetAddresses = addresses

	return reported
}

// refreshBuckets runs one refresh of the routing table: a lookup for the own Peer ID, and a lookup for a random ID in every bucket that holds fewer peers than the bucket size,
// from the bucket of the farthest peers to the bucket of the closest peer in the routing table.
func (backend *Backend) refreshBuckets() {
	if err := backend.nodesDHT.RefreshBuckets(); err != nil {
		backend.LogError("refreshBuckets", "error refreshing the routing table: %v\n", err)
	}
}

// autoBucketRefresh refreshes the routing table every 5 minutes.
func (backend *Backend) autoBucketRefresh() {
	for {
		time.Sleep(bucketRefreshTime)

		backend.refreshBuckets()
	}
}

// bootstrapKademlia bootstraps the Kademlia bucket list
func (backend *Backend) bootstrapKademlia() {
	// The channel keeps one peer that is added while this function is not waiting.
	monitor := make(chan *PeerInfo, 1)
	backend.registerPeerMonitor(monitor)

	// Wait until the routing table holds a peer. It may hold one already.
	for backend.nodesDHT.NumNodes() < 1 {
		<-monitor
	}

	backend.unregisterPeerMonitor(monitor)

	// Refresh every second 3 times
	for n := 0; n < 3; n++ {
		backend.refreshBuckets()

		time.Sleep(time.Second)
	}
}

// Future sendAnnouncementX: If it detects that announcements are sent out to the same peer within 50ms it should activate a wait-and-group scheme.

// sendAnnouncementFindPeer sends A_FIND_PEER for the requested key, or A_FIND_SELF if the key is self.
func (peer *PeerInfo) sendAnnouncementFindPeer(request *dht.InformationRequest) {
	if bytes.Equal(request.Key, peer.Backend.peerPublicKey) {
		peer.sendAnnouncement(false, &protocol.Requests{FindSelf: true}, request)
	} else {
		peer.sendAnnouncement(false, &protocol.Requests{FindPeer: [][]byte{request.Key}}, request)
	}
}

// ---- CORE DATA FUNCTIONS ----

// Data2Hash returns the hash for the data
func Data2Hash(data []byte) (hash []byte) {
	return protocol.HashData(data)
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

// FindNode finds a peer via the DHT. A peer that is found answered the lookup. If it answered via a relay only, it is not in the peer list and the returned peer is a virtual one.
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

	if peer = backend.PeerlistLookup(publicKey); peer != nil {
		return node, peer, err
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
