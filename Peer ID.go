/*
File Name:  Peer ID.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"encoding/hex"
	"errors"
	"math/rand"
	"net"
	"os"
	"sync"

	"github.com/PeernetOfficial/core/btcec"
	"github.com/PeernetOfficial/core/dht"
	"github.com/PeernetOfficial/core/protocol"
)

// peerID is the current peers ID. It is a ECDSA (secp256k1) 257-bit public key.
// The node ID is the blake3 hash of the public key compressed form.
var peerPrivateKey *btcec.PrivateKey
var peerPublicKey *btcec.PublicKey
var nodeID []byte

func initPeerID() {
	peerList = make(map[[btcec.PubKeyBytesLenCompressed]byte]*PeerInfo)

	// load existing key from config, if available
	if len(config.PrivateKey) > 0 {
		configPK, err := hex.DecodeString(config.PrivateKey)
		if err == nil {
			peerPrivateKey, peerPublicKey = btcec.PrivKeyFromBytes(btcec.S256(), configPK)
			nodeID = protocol.PublicKey2NodeID(peerPublicKey)

			if config.AutoUpdateSeedList {
				configUpdateSeedList()
			}
			return
		}

		Filters.LogError("initPeerID", "private key in config is corrupted! Error: %s\n", err.Error())
		os.Exit(ExitPrivateKeyCorrupt)
	}

	// if the peer ID is empty, create a new user public-private key pair
	var err error
	peerPrivateKey, peerPublicKey, err = Secp256k1NewPrivateKey()
	if err != nil {
		Filters.LogError("initPeerID", "generating public-private key pairs: %s\n", err.Error())
		os.Exit(ExitPrivateKeyCreate)
	}
	nodeID = protocol.PublicKey2NodeID(peerPublicKey)

	// save the newly generated private key into the config
	config.PrivateKey = hex.EncodeToString(peerPrivateKey.Serialize())

	saveConfig()
}

// Secp256k1NewPrivateKey creates a new public-private key pair
func Secp256k1NewPrivateKey() (privateKey *btcec.PrivateKey, publicKey *btcec.PublicKey, err error) {
	key, err := btcec.NewPrivateKey(btcec.S256())
	if err != nil {
		return nil, nil, err
	}

	return key, (*btcec.PublicKey)(&key.PublicKey), nil
}

// ExportPrivateKey returns the peers public and private key
func ExportPrivateKey() (privateKey *btcec.PrivateKey, publicKey *btcec.PublicKey) {
	return peerPrivateKey, peerPublicKey
}

// SelfNodeID returns the node ID used for DHT
func SelfNodeID() []byte {
	return nodeID
}

// SelfUserAgent returns the User Agent
func SelfUserAgent() string {
	return userAgent
}

// PeerInfo stores information about a single remote peer
type PeerInfo struct {
	PublicKey          *btcec.PublicKey // Public key
	NodeID             []byte           // Node ID in Kademlia network = blake3(Public Key).
	connectionActive   []*Connection    // List of active established connections to the peer.
	connectionInactive []*Connection    // List of former connections that are no longer valid. They may be removed after a while.
	connectionLatest   *Connection      // Latest valid connection.
	sync.RWMutex                        // Mutex for access to list of connections.
	messageSequence    uint32           // Sequence number. Increased with every message.
	IsRootPeer         bool             // Whether the peer is a trusted root peer.
	UserAgent          string           // User Agent reported by remote peer. Empty if no Announcement/Response message was yet received.
	Features           uint8            // Feature bit array. 0 = IPv4_LISTEN, 1 = IPv6_LISTEN, 1 = FIREWALL
	isVirtual          bool             // Whether it is a virtual peer for establishing a connection.
	targetAddresses    []*peerAddress   // Virtual peer: Addresses to send any replies.
	traversePeer       *PeerInfo        // Virtual peer: Same field as in connection.
	BlockchainHeight   uint32           // Blockchain height
	BlockchainVersion  uint64           // Blockchain version

	// statistics
	StatsPacketSent     uint64 // Count of packets sent
	StatsPacketReceived uint64 // Count of packets received
}

type peerAddress struct {
	IP           net.IP
	Port         uint16
	PortInternal uint16
}

// peerList keeps track of all peers
var peerList map[[btcec.PubKeyBytesLenCompressed]byte]*PeerInfo
var peerlistMutex sync.RWMutex

// PeerlistAdd adds a new peer to the peer list. It does not validate the peer info. If the peer is already added, it does nothing. Connections must be live.
func PeerlistAdd(PublicKey *btcec.PublicKey, connections ...*Connection) (peer *PeerInfo, added bool) {
	if len(connections) == 0 {
		return nil, false
	}
	publicKeyCompressed := publicKey2Compressed(PublicKey)

	peerlistMutex.Lock()
	defer peerlistMutex.Unlock()

	peer, ok := peerList[publicKeyCompressed]
	if ok {
		return peer, false
	}

	peer = &PeerInfo{PublicKey: PublicKey, connectionActive: connections, connectionLatest: connections[0], NodeID: protocol.PublicKey2NodeID(PublicKey), messageSequence: rand.Uint32()}
	_, peer.IsRootPeer = rootPeers[publicKeyCompressed]

	peerList[publicKeyCompressed] = peer

	// add to Kademlia
	nodesDHT.AddNode(&dht.Node{ID: peer.NodeID, Info: peer})

	// TODO: If the node isn't added to Kademlia, it should be either added temporarily to the peerList with an expiration, or to a temp list, or not at all.

	// send to all channels non-blocking
	for _, monitor := range peerMonitor {
		select {
		case monitor <- peer:
		default:
		}
	}

	Filters.NewPeer(peer, connections[0])
	Filters.NewPeerConnection(peer, connections[0])

	return peer, true
}

// PeerlistRemove removes a peer from the peer list.
func PeerlistRemove(peer *PeerInfo) {
	peerlistMutex.Lock()
	defer peerlistMutex.Unlock()

	// remove from Kademlia
	nodesDHT.RemoveNode(peer.NodeID)

	delete(peerList, publicKey2Compressed(peer.PublicKey))
}

// PeerlistGet returns the full peer list
func PeerlistGet() (peers []*PeerInfo) {
	peerlistMutex.RLock()
	defer peerlistMutex.RUnlock()

	for _, peer := range peerList {
		peers = append(peers, peer)
	}

	return peers
}

// PeerlistLookup returns the peer from the list with the public key
func PeerlistLookup(publicKey *btcec.PublicKey) (peer *PeerInfo) {
	peerlistMutex.RLock()
	defer peerlistMutex.RUnlock()

	return peerList[publicKey2Compressed(publicKey)]
}

// PeerlistCount returns the current count of peers in the peer list
func PeerlistCount() (count int) {
	peerlistMutex.RLock()
	defer peerlistMutex.RUnlock()

	return len(peerList)
}

func publicKey2Compressed(publicKey *btcec.PublicKey) [btcec.PubKeyBytesLenCompressed]byte {
	var key [btcec.PubKeyBytesLenCompressed]byte
	copy(key[:], publicKey.SerializeCompressed())
	return key
}

// records2Nodes translates infoPeer structures to nodes. If the reported nodes are not in the peer table, it will create temporary PeerInfo structures.
// LastContact is passed on in the Node.LastSeen field.
func records2Nodes(records []protocol.PeerRecord, peerSource *PeerInfo) (nodes []*dht.Node) {
	for _, record := range records {
		if isReturnedPeerBadQuality(&record) {
			continue
		}

		var peer *PeerInfo
		if record.PublicKey.IsEqual(peerSource.PublicKey) {
			// Special case if peer that stores info = sender. In that case IP:Port in the record would be empty anyway.
			peer = peerSource
		} else if peer = PeerlistLookup(record.PublicKey); peer == nil {
			// Create temporary peer which is not added to the global list and not added to Kademlia.
			// traversePeer is set to the peer who provided the node information.
			addresses := peerRecordToAddresses(&record)
			if len(addresses) == 0 {
				continue
			}

			peer = &PeerInfo{PublicKey: record.PublicKey, connectionActive: nil, connectionLatest: nil, NodeID: protocol.PublicKey2NodeID(record.PublicKey), messageSequence: rand.Uint32(), isVirtual: true, targetAddresses: addresses, traversePeer: peerSource, Features: record.Features}
		}

		nodes = append(nodes, &dht.Node{ID: peer.NodeID, LastSeen: record.LastContactT, Info: peer})
	}

	return
}

// selfPeerRecord returns self as peer record
func selfPeerRecord() (result protocol.PeerRecord) {
	return protocol.PeerRecord{
		PublicKey: peerPublicKey,
		NodeID:    nodeID,
		//IP:          network.address.IP,
		//Port:        uint16(network.address.Port),
		LastContact: 0,
		Features:    FeatureSupport(),
	}
}

var peerMonitor []chan<- *PeerInfo

// registerPeerMonitor registers a channel to receive all new peers
func registerPeerMonitor(channel chan<- *PeerInfo) {
	peerlistMutex.Lock()
	defer peerlistMutex.Unlock()

	peerMonitor = append(peerMonitor, channel)
}

// unregisterPeerMonitor unregisters a channel
func unregisterPeerMonitor(channel chan<- *PeerInfo) {
	peerlistMutex.Lock()
	defer peerlistMutex.Unlock()

	for n, channel2 := range peerMonitor {
		if channel == channel2 {
			peerMonitorNew := peerMonitor[:n]
			if n < len(peerMonitor)-1 {
				peerMonitorNew = append(peerMonitorNew, peerMonitor[n+1:]...)
			}
			peerMonitor = peerMonitorNew
			break
		}
	}
}

// DeleteAccount deletes the account
func DeleteAccount() {
	// delete the blockchain
	UserBlockchain.DeleteBlockchain()

	// delete the warehouse
	UserWarehouse.DeleteWarehouse()

	// delete the private key
	config.PrivateKey = ""
	saveConfig()
}

// PublicKeyFromPeerID decodes the peer ID (hex encoded) into a public key.
func PublicKeyFromPeerID(peerID string) (publicKey *btcec.PublicKey, err error) {
	hash, err := hex.DecodeString(peerID)
	if err != nil || len(hash) != 33 {
		return nil, errors.New("invalid peer ID length")
	}

	return btcec.ParsePubKey(hash, btcec.S256())
}
