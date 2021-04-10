/*
File Name:  Peer ID.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"encoding/hex"
	"log"
	"net"
	"os"
	"sync"

	"github.com/PeernetOfficial/core/dht"
	"github.com/btcsuite/btcd/btcec"
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
			nodeID = publicKey2NodeID(peerPublicKey)
			return
		}

		log.Printf("Private key in config is corrupted! Error: %s\n", err.Error())
		os.Exit(1)
	}

	// if the peer ID is empty, create a new user public-private key pair
	var err error
	peerPrivateKey, peerPublicKey, err = Secp256k1NewPrivateKey()
	if err != nil {
		log.Printf("Error generating public-private key pairs: %s\n", err.Error())
		os.Exit(1)
	}
	nodeID = publicKey2NodeID(peerPublicKey)

	// save the newly generated private key into the config
	config.PrivateKey = hex.EncodeToString(peerPublicKey.SerializeCompressed())

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

// PeerInfo stores information about a single remote peer
type PeerInfo struct {
	PublicKey          *btcec.PublicKey // Public key
	NodeID             []byte           // Node ID in Kademlia network = blake3(Public Key).
	connectionActive   []*Connection    // List of active established connections to the peer.
	connectionInactive []*Connection    // List of former connections that are no longer valid. They may be removed after a while.
	connectionLatest   *Connection      // Latest valid connection.
	sync.RWMutex                        // Mutex for access to list of connections.

	// statistics
	StatsPacketSent     uint64 // Count of packets sent
	StatsPacketReceived uint64 // Count of packets received
}

var peerList map[[btcec.PubKeyBytesLenCompressed]byte]*PeerInfo
var peerlistMutex sync.RWMutex

// PeerlistAdd adds a new peer to the peer list. It does not validate the peer info. If the peer is already added, it does nothing. Connections must be live.
func PeerlistAdd(PublicKey *btcec.PublicKey, connections ...*Connection) (peer *PeerInfo, added bool) {
	if len(connections) == 0 {
		return nil, false
	}

	peerlistMutex.Lock()
	defer peerlistMutex.Unlock()

	peer, ok := peerList[publicKey2Compressed(PublicKey)]
	if ok {
		return peer, false
	}

	peer = &PeerInfo{PublicKey: PublicKey, connectionActive: connections, connectionLatest: connections[0], NodeID: publicKey2NodeID(PublicKey)}
	peerList[publicKey2Compressed(peer.PublicKey)] = peer

	// add to Kademlia
	nodesDHT.AddNode(&dht.Node{ID: peer.NodeID, Info: peer})

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

	peer, _ = peerList[publicKey2Compressed(publicKey)]
	return peer
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

// publicKey2NodeID translates the Public Key into the node ID used in the Kademlia network.
// This is very important for lookup of data in the DHT.
func publicKey2NodeID(publicKey *btcec.PublicKey) (nodeID []byte) {
	return hashData(publicKey.SerializeCompressed())
}

// record2Peer translate a peer record (from a message) into an actual usable PeerInfo structure
// It requires the network parameter which must be the same as caller/supplier. This ensures that peer details do not "jump" between physical network adapters.
func record2Peer(record PeerRecord, network *Network) (peerN *PeerInfo) {
	if peerN = PeerlistLookup(record.PublicKey); peerN != nil {
		return peerN
	}

	// Create temporary peer which is not added to the global list and not added to Kademlia.
	connection := &Connection{Network: network, Address: &net.UDPAddr{IP: record.IP, Port: int(record.Port)}, Status: ConnectionActive}
	return &PeerInfo{PublicKey: record.PublicKey, connectionActive: []*Connection{connection}, connectionLatest: connection, NodeID: publicKey2NodeID(record.PublicKey)}
}

// records2Nodes translates infoPeer structures to nodes
// LastContact is passed on in the Node.LastSeen field.
func records2Nodes(records []PeerRecord, network *Network) (nodes []*dht.Node) {
	for _, record := range records {
		peer := record2Peer(record, network)
		nodes = append(nodes, &dht.Node{ID: peer.NodeID, LastSeen: lastContact2Time(record.LastContact), Info: peer})
	}

	return
}

// selfPeerRecord returns self as peer record
func selfPeerRecord(network *Network) (result PeerRecord) {
	return PeerRecord{
		PublicKey:   peerPublicKey,
		NodeID:      nodeID,
		IP:          network.address.IP,
		Port:        uint16(network.address.Port),
		LastContact: 0,
	}
}
