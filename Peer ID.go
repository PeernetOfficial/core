/*
File Name:  Peer ID.go
Copyright:  2021 Kleissner Ventures Ltd
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
	"sync/atomic"
	"time"

	"github.com/PeernetOfficial/core/dht"
	"github.com/PeernetOfficial/core/protocol"
)

func (backend *Backend) initPeerID() {
	backend.peerList = make(map[[ed25519.PublicKeySize]byte]*PeerInfo)

	// load existing key from config, if available
	if len(backend.Config.PrivateKey) > 0 {
		configPK, err := hex.DecodeString(backend.Config.PrivateKey)
		if err == nil && len(configPK) != ed25519.SeedSize {
			err = errors.New("invalid length")
		}
		if err == nil {
			backend.peerPrivateKey = ed25519.NewKeyFromSeed(configPK)
			backend.peerPublicKey = backend.peerPrivateKey.Public().(ed25519.PublicKey)
			backend.peerKeyPair, err = protocol.NewKeyPair(backend.peerPrivateKey)
		}
		if err == nil {
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
	if err == nil {
		backend.peerKeyPair, err = protocol.NewKeyPair(backend.peerPrivateKey)
	}
	if err != nil {
		backend.LogError("initPeerID", "generating public-private key pairs: %s\n", err.Error())
		os.Exit(ExitPrivateKeyCreate)
	}

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

// SelfPublicKey returns the public key (= peer ID) of this peer. It is also the peer's key in the DHT.
func (backend *Backend) SelfPublicKey() ed25519.PublicKey {
	return backend.peerPublicKey
}

// SelfUserAgent returns the User Agent
func (backend *Backend) SelfUserAgent() string {
	return backend.userAgent
}

// PeerInfo stores information about a single remote peer
type PeerInfo struct {
	PublicKey             ed25519.PublicKey // Public key = Peer ID. It is also the peer's key in the DHT.
	connectionActive      []*Connection    // List of active established connections to the peer.
	connectionInactive    []*Connection    // List of former connections that are no longer valid. They may be removed after a while.
	connectionLatest      *Connection      // Latest valid connection.
	sync.RWMutex                           // Mutex for access to list of connections.
	messageSequence       uint32           // Sequence number. Increased with every message.
	IsRootPeer            bool             // Whether the peer is a trusted root peer.
	UserAgent             string           // User Agent reported by remote peer. Empty if no Announcement/Response message was yet received.
	Features              uint8            // Feature bit array. 0 = IPv4_LISTEN, 1 = IPv6_LISTEN
	isVirtual             bool             // Whether it is a virtual peer for establishing a connection.
	targetAddresses       []*peerAddress   // Virtual peer: Addresses to send any replies.
	traversePeer          *PeerInfo        // Virtual peer: Same field as in connection.
	relayPeer             *PeerInfo        // Virtual peer: The peer that relays packets to and from this peer. Packets to this peer are sent in Relay messages via it.
	BlockchainHeight      uint64           // Blockchain height
	BlockchainVersion     uint64           // Blockchain version
	blockchainLastRefresh time.Time        // Last refresh of the blockchain info.
	packetKeys            protocol.PacketKeys // Packet keys for packets sent to and received from the peer.
	transfers             int32            // Count of running transfers with the peer.

	// copy of the blockchain of the peer in the blockchain cache
	blockchainCacheMutex   sync.Mutex // Mutex for the fields that follow.
	blockchainCacheRunning bool       // Whether an update of the copy is running.
	blockchainCacheChanged bool       // Whether the peer reported a change of the blockchain version or height that no update was started for yet.
	blockchainCacheLast    time.Time  // Start of the last update of the copy.

	// Time the blockchain of the peer was last reported to the client.
	blockchainCacheReported time.Time

	// statistics
	StatsPacketSent     uint64 // Count of packets sent
	StatsPacketReceived uint64 // Count of packets received

	Backend *Backend
}

type peerAddress struct {
	IP           net.IP
	Port         uint16 // Port observed by the peer that reported the address.
	PortInternal uint16 // Internal port as reported by the remote peer. 0 if not known.
	PortExternal uint16 // External port as reported by the remote peer. 0 if not known.
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

	peer = &PeerInfo{Backend: backend, PublicKey: PublicKey, connectionActive: connections, connectionLatest: connections[0], messageSequence: mathrand.Uint32()}
	_, peer.IsRootPeer = rootPeers[publicKeyArray]

	backend.peerList[publicKeyArray] = peer

	// add to Kademlia. A peer that is not taken into the routing table is a passive peer.
	backend.nodesDHT.AddNode(&dht.Node{ID: peer.PublicKey, Info: peer})

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

// IsActive checks if the peer is an active peer: it is in the routing table, or a transfer with it is running. Every other peer in the peer list is a passive peer.
// Active peers are pinged. A passive peer stays in the peer list as long as its packets arrive.
func (peer *PeerInfo) IsActive() bool {
	return peer.Backend.nodesDHT.IsNodeContact(peer.PublicKey) != nil || atomic.LoadInt32(&peer.transfers) > 0
}

// routingTableAdd adds a peer of the peer list to the routing table, if it is not in it and its bucket has a free place.
func (backend *Backend) routingTableAdd(peer *PeerInfo) {
	if backend.nodesDHT.IsNodeContact(peer.PublicKey) != nil {
		return
	}

	// The peer list is locked so that the peer is not removed from it at the same time.
	backend.peerlistMutex.RLock()
	defer backend.peerlistMutex.RUnlock()

	if backend.peerList[publicKey2Array(peer.PublicKey)] == peer {
		backend.nodesDHT.AddNode(&dht.Node{ID: peer.PublicKey, Info: peer})
	}
}

// PeerlistRemove removes a peer from the peer list.
func (backend *Backend) PeerlistRemove(peer *PeerInfo) {
	backend.peerlistMutex.Lock()
	defer backend.peerlistMutex.Unlock()

	// remove from Kademlia
	backend.nodesDHT.RemoveNode(peer.PublicKey)

	delete(backend.peerList, publicKey2Array(peer.PublicKey))
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

// packetKeys returns the cache of the packet keys of the peer with the public key.
// It is nil if the peer is not in the peer list. The packet key is then derived for the packet.
func (backend *Backend) packetKeys(publicKey ed25519.PublicKey) (keys *protocol.PacketKeys) {
	if peer := backend.PeerlistLookup(publicKey); peer != nil {
		return &peer.packetKeys
	}
	return nil
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
// allowLocal indicates whether the records were received via a local connection. Local addresses of the reported nodes are only used in that case.
func (peerSource *PeerInfo) records2Nodes(records []protocol.PeerRecord, allowLocal bool) (nodes []*dht.Node) {
	for _, record := range records {
		var peer *PeerInfo
		if record.PublicKey.Equal(peerSource.PublicKey) {
			// The sender itself: a peer that stores the requested data lists its own Peer ID as storing peer record without an address. It is reached via the connection the response came from.
			peer = peerSource
		} else if peerSource.Backend.isReturnedPeerBadQuality(&record) {
			continue
		} else if peer = peerSource.Backend.PeerlistLookup(record.PublicKey); peer == nil {
			// Create temporary peer which is not added to the global list and not added to Kademlia.
			// traversePeer is set to the peer who provided the node information, see reportingPeer.
			addresses := peerRecordToAddresses(&record, allowLocal)
			if len(addresses) == 0 {
				continue
			}

			peer = &PeerInfo{Backend: peerSource.Backend, PublicKey: record.PublicKey, connectionActive: nil, connectionLatest: nil, messageSequence: mathrand.Uint32(), isVirtual: true, targetAddresses: addresses, traversePeer: peerSource.reportingPeer(), Features: record.Features}
		}

		nodes = append(nodes, &dht.Node{ID: peer.PublicKey, LastSeen: time.Now(), Info: peer})
	}

	return
}

// storing2Nodes translates the storing peer records that the peer reported for a blockchain to nodes. The Info of a node is a *BlockchainStoring.
// A storing peer record whose blockchain record is not valid is ignored.
func (peerSource *PeerInfo) storing2Nodes(blockchainPublicKey ed25519.PublicKey, records []protocol.StoringPeer, allowLocal bool) (nodes []*dht.Node) {
	now := time.Now()

	for n := range records {
		record := &records[n]
		if !protocol.BlockchainRecordValid(record.PublicKey, blockchainPublicKey, record.BlockchainVersion, record.BlockchainHeight, record.Time, record.Signature, now) {
			continue
		}

		for _, node := range peerSource.records2Nodes([]protocol.PeerRecord{record.PeerRecord}, allowLocal) {
			storing := &BlockchainStoring{Peer: node.Info.(*PeerInfo), Version: record.BlockchainVersion, Height: record.BlockchainHeight, Time: time.Unix(int64(record.Time), 0)}
			nodes = append(nodes, &dht.Node{ID: node.ID, LastSeen: node.LastSeen, Info: storing})
		}
	}

	return nodes
}

// reportingPeer returns the peer as the one that forwards Traverse messages to the peers it reported.
// It is nil if the peer is reached via a relay: only Announcement and Response messages are sent via a relay.
func (peer *PeerInfo) reportingPeer() *PeerInfo {
	if peer.relayPeer != nil {
		return nil
	}

	return peer
}

// selfPeerRecord returns self as storing peer record. It carries no address: the receiver reaches this peer via the connection the response comes from.
func (backend *Backend) selfPeerRecord() (result protocol.PeerRecord) {
	return protocol.PeerRecord{
		PublicKey: backend.peerPublicKey,
		Features:  backend.FeatureSupport(),
	}
}

// selfStoringPeer returns self as storing peer of the blockchain: the peer record of self and the blockchain record, signed at the current time.
func (backend *Backend) selfStoringPeer(blockchainPublicKey ed25519.PublicKey, version, height uint64) (result protocol.StoringPeer) {
	recordTime := uint32(time.Now().Unix())

	return protocol.StoringPeer{
		PeerRecord:        backend.selfPeerRecord(),
		BlockchainVersion: version,
		BlockchainHeight:  height,
		Time:              recordTime,
		Signature:         protocol.BlockchainRecordSign(backend.peerPrivateKey, blockchainPublicKey, version, height, recordTime),
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
