/*
File Name:  Peer ID.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	mathrand "math/rand"
	"net"
	"os"
	"sync"
	"time"

	"github.com/PeernetOfficial/core/dht"
	"github.com/PeernetOfficial/core/protocol"
)

func (backend *Backend) initPeerID() {
	backend.peerList = make(map[[ed25519.PublicKeySize]byte]*PeerInfo)
	backend.nodeList = make(map[[protocol.HashSize]byte]*PeerInfo)

	// load existing key from config, if available
	if len(backend.Config.PrivateKey) > 0 {
		configPK, err := hex.DecodeString(backend.Config.PrivateKey)
		if err == nil && len(configPK) != ed25519.SeedSize {
			err = errors.New("invalid length")
		}
		if err == nil {
			backend.peerPrivateKey = ed25519.NewKeyFromSeed(configPK)
			backend.peerPublicKey = backend.peerPrivateKey.Public().(ed25519.PublicKey)
			backend.nodeID = protocol.PublicKey2NodeID(backend.peerPublicKey)

			if backend.Config.AutoUpdateSeedList {
				backend.configUpdateSeedList()
			}
			return
		}

		backend.LogError("initPeerID", "private key in config is corrupted! Error: %s\n", err.Error())
		os.Exit(ExitPrivateKeyCorrupt)
	}

	// if the peer ID is empty, create a new user public-private key pair
	var err error
	backend.peerPublicKey, backend.peerPrivateKey, err = NewPrivateKey()
	if err != nil {
		backend.LogError("initPeerID", "generating public-private key pairs: %s\n", err.Error())
		os.Exit(ExitPrivateKeyCreate)
	}
	backend.nodeID = protocol.PublicKey2NodeID(backend.peerPublicKey)

	// save the newly generated private key into the config
	backend.Config.PrivateKey = hex.EncodeToString(backend.peerPrivateKey.Seed())

	backend.SaveConfig()
}

// NewPrivateKey creates a new Ed25519 public-private key pair
func NewPrivateKey() (publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey, err error) {
	return ed25519.GenerateKey(rand.Reader)
}

// ExportPrivateKey returns the peers public and private key
func (backend *Backend) ExportPrivateKey() (privateKey ed25519.PrivateKey, publicKey ed25519.PublicKey) {
	return backend.peerPrivateKey, backend.peerPublicKey
}

// SelfNodeID returns the node ID used for DHT
func (backend *Backend) SelfNodeID() []byte {
	return backend.nodeID
}

// SelfUserAgent returns the User Agent
func (backend *Backend) SelfUserAgent() string {
	return backend.userAgent
}

// PeerInfo stores information about a single remote peer
type PeerInfo struct {
	PublicKey             ed25519.PublicKey // Public key
	NodeID                []byte           // Node ID in Kademlia network = blake3(Public Key).
	connectionActive      []*Connection    // List of active established connections to the peer.
	connectionInactive    []*Connection    // List of former connections that are no longer valid. They may be removed after a while.
	connectionLatest      *Connection      // Latest valid connection.
	sync.RWMutex                           // Mutex for access to list of connections.
	messageSequence       uint32           // Sequence number. Increased with every message.
	IsRootPeer            bool             // Whether the peer is a trusted root peer.
	UserAgent             string           // User Agent reported by remote peer. Empty if no Announcement/Response message was yet received.
	Features              uint8            // Feature bit array. 0 = IPv4_LISTEN, 1 = IPv6_LISTEN, 1 = FIREWALL
	isVirtual             bool             // Whether it is a virtual peer for establishing a connection.
	targetAddresses       []*peerAddress   // Virtual peer: Addresses to send any replies.
	traversePeer          *PeerInfo        // Virtual peer: Same field as in connection.
	BlockchainHeight      uint64           // Blockchain height
	BlockchainVersion     uint64           // Blockchain version
	blockchainLastRefresh time.Time        // Last refresh of the blockchain info.

	// statistics
	StatsPacketSent     uint64 // Count of packets sent
	StatsPacketReceived uint64 // Count of packets received

	Backend *Backend
}

type peerAddress struct {
	IP           net.IP
	Port         uint16
	PortInternal uint16
}

// PeerlistAdd adds a new peer to the peer list. It does not validate the peer info. If the peer is already added, it does nothing. Connections must be live.
func (backend *Backend) PeerlistAdd(PublicKey ed25519.PublicKey, connections ...*Connection) (peer *PeerInfo, added bool) {
	if len(connections) == 0 {
		return nil, false
	}
	publicKeyArray := publicKey2Array(PublicKey)

	backend.peerlistMutex.Lock()
	defer backend.peerlistMutex.Unlock()

	peer, ok := backend.peerList[publicKeyArray]
	if ok {
		return peer, false
	}

	peer = &PeerInfo{Backend: backend, PublicKey: PublicKey, connectionActive: connections, connectionLatest: connections[0], NodeID: protocol.PublicKey2NodeID(PublicKey), messageSequence: mathrand.Uint32()}
	_, peer.IsRootPeer = rootPeers[publicKeyArray]

	backend.peerList[publicKeyArray] = peer

	// also add to mirrored nodeList
	var nodeID [protocol.HashSize]byte
	copy(nodeID[:], peer.NodeID)
	backend.nodeList[nodeID] = peer

	// add to Kademlia
	backend.nodesDHT.AddNode(&dht.Node{ID: peer.NodeID, Info: peer})

	// TODO: If the node isn't added to Kademlia, it should be either added temporarily to the peerList with an expiration, or to a temp list, or not at all.

	// send to all channels non-blocking
	for _, monitor := range backend.peerMonitor {
		select {
		case monitor <- peer:
		default:
		}
	}

	backend.Filters.NewPeer(peer, connections[0])
	backend.Filters.NewPeerConnection(peer, connections[0])

	return peer, true
}

// PeerlistRemove removes a peer from the peer list.
func (backend *Backend) PeerlistRemove(peer *PeerInfo) {
	backend.peerlistMutex.Lock()
	defer backend.peerlistMutex.Unlock()

	// remove from Kademlia
	backend.nodesDHT.RemoveNode(peer.NodeID)

	delete(backend.peerList, publicKey2Array(peer.PublicKey))

	var nodeID [protocol.HashSize]byte
	copy(nodeID[:], peer.NodeID)

	delete(backend.nodeList, nodeID)
}

// PeerlistGet returns the full peer list
func (backend *Backend) PeerlistGet() (peers []*PeerInfo) {
	backend.peerlistMutex.RLock()
	defer backend.peerlistMutex.RUnlock()

	for _, peer := range backend.peerList {
		peers = append(peers, peer)
	}

	return peers
}

// PeerlistLookup returns the peer from the list with the public key
func (backend *Backend) PeerlistLookup(publicKey ed25519.PublicKey) (peer *PeerInfo) {
	backend.peerlistMutex.RLock()
	defer backend.peerlistMutex.RUnlock()

	return backend.peerList[publicKey2Array(publicKey)]
}

// NodelistLookup returns the peer from the list with the node ID
func (backend *Backend) NodelistLookup(nodeID []byte) (peer *PeerInfo) {
	backend.peerlistMutex.RLock()
	defer backend.peerlistMutex.RUnlock()

	var nodeID2 [protocol.HashSize]byte
	copy(nodeID2[:], nodeID)

	return backend.nodeList[nodeID2]
}

// PeerlistCount returns the current count of peers in the peer list
func (backend *Backend) PeerlistCount() (count int) {
	backend.peerlistMutex.RLock()
	defer backend.peerlistMutex.RUnlock()

	return len(backend.peerList)
}

// publicKey2Array returns the public key as fixed-size array, usable as map key.
func publicKey2Array(publicKey ed25519.PublicKey) [ed25519.PublicKeySize]byte {
	var key [ed25519.PublicKeySize]byte
	copy(key[:], publicKey)
	return key
}

// records2Nodes translates infoPeer structures to nodes reported by the peer. If the reported nodes are not in the peer table, it will create temporary PeerInfo structures.
// LastContact is passed on in the Node.LastSeen field.
func (peerSource *PeerInfo) records2Nodes(records []protocol.PeerRecord) (nodes []*dht.Node) {
	for _, record := range records {
		if peerSource.Backend.isReturnedPeerBadQuality(&record) {
			continue
		}

		var peer *PeerInfo
		if record.PublicKey.Equal(peerSource.PublicKey) {
			// Special case if peer that stores info = sender. In that case IP:Port in the record would be empty anyway.
			peer = peerSource
		} else if peer = peerSource.Backend.PeerlistLookup(record.PublicKey); peer == nil {
			// Create temporary peer which is not added to the global list and not added to Kademlia.
			// traversePeer is set to the peer who provided the node information.
			addresses := peerRecordToAddresses(&record)
			if len(addresses) == 0 {
				continue
			}

			peer = &PeerInfo{Backend: peerSource.Backend, PublicKey: record.PublicKey, connectionActive: nil, connectionLatest: nil, NodeID: protocol.PublicKey2NodeID(record.PublicKey), messageSequence: mathrand.Uint32(), isVirtual: true, targetAddresses: addresses, traversePeer: peerSource, Features: record.Features}
		}

		nodes = append(nodes, &dht.Node{ID: peer.NodeID, LastSeen: record.LastContactT, Info: peer})
	}

	return
}

// selfPeerRecord returns self as peer record
func (backend *Backend) selfPeerRecord() (result protocol.PeerRecord) {
	return protocol.PeerRecord{
		PublicKey: backend.peerPublicKey,
		NodeID:    backend.nodeID,
		//IP:          network.address.IP,
		//Port:        uint16(network.address.Port),
		LastContact: 0,
		Features:    backend.FeatureSupport(),
	}
}

// registerPeerMonitor registers a channel to receive all new peers
func (backend *Backend) registerPeerMonitor(channel chan<- *PeerInfo) {
	backend.peerlistMutex.Lock()
	defer backend.peerlistMutex.Unlock()

	backend.peerMonitor = append(backend.peerMonitor, channel)
}

// unregisterPeerMonitor unregisters a channel
func (backend *Backend) unregisterPeerMonitor(channel chan<- *PeerInfo) {
	backend.peerlistMutex.Lock()
	defer backend.peerlistMutex.Unlock()

	for n, channel2 := range backend.peerMonitor {
		if channel == channel2 {
			peerMonitorNew := backend.peerMonitor[:n]
			if n < len(backend.peerMonitor)-1 {
				peerMonitorNew = append(peerMonitorNew, backend.peerMonitor[n+1:]...)
			}
			backend.peerMonitor = peerMonitorNew
			break
		}
	}
}

// DeleteAccount deletes the account
func (backend *Backend) DeleteAccount() {
	// delete the blockchain
	backend.UserBlockchain.DeleteBlockchain()

	// delete the warehouse
	backend.UserWarehouse.DeleteWarehouse()

	// delete the private key
	backend.Config.PrivateKey = ""
	backend.SaveConfig()
}

// PublicKeyFromPeerID decodes the peer ID (hex encoded) into a public key.
func PublicKeyFromPeerID(peerID string) (publicKey ed25519.PublicKey, err error) {
	publicKey, err = hex.DecodeString(peerID)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("invalid peer ID length")
	}

	return publicKey, nil
}
