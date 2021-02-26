/*
File Name:  Peer ID.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"encoding/hex"
	"errors"
	"log"
	"net"
	"os"
	"sync"
	"sync/atomic"

	"github.com/btcsuite/btcd/btcec"
)

// peerID is the current peers ID. It is a ECDSA (secp256k1) 257-bit public key.
var peerPrivateKey *btcec.PrivateKey
var peerPublicKey *btcec.PublicKey

func initPeerID() {
	peerList = make(map[[btcec.PubKeyBytesLenCompressed]byte]*PeerInfo)

	// load existing key from config, if available
	if len(config.PrivateKey) > 0 {
		configPK, err := hex.DecodeString(config.PrivateKey)
		if err == nil {
			peerPrivateKey, peerPublicKey = btcec.PrivKeyFromBytes(btcec.S256(), configPK)
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

// Connection is an established connection between a remote IP address and a local network adapter.
// New connections may only be created in case of successful INCOMING packets.
type Connection struct {
	Network *Network     // network which received the packet
	Address *net.UDPAddr // address of the sender or receiver
}

// PeerInfo stores information about a single remote peer
type PeerInfo struct {
	PublicKey        *btcec.PublicKey // Public key
	Connections      []*Connection    // list of established connections to the peer
	connectionLatest *Connection      // Latest valid connection. Allows to switch on the fly between connections; if one becomes invalid, try others.

	// statistics
	StatsPacketSent     uint64 // Count of packets sent
	StatsPacketReceived uint64 // Count of packets received
}

var peerList map[[btcec.PubKeyBytesLenCompressed]byte]*PeerInfo
var peerlistMutex sync.RWMutex

// PeerlistAdd adds a new peer to the peer list. It does not validate the peer info. If the peer is already added, it does nothing.
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

	peer = &PeerInfo{PublicKey: PublicKey, Connections: connections, connectionLatest: connections[0]}
	peerList[publicKey2Compressed(peer.PublicKey)] = peer

	return peer, true
}

// PeerlistRemove removes a peer from the peer list.
func PeerlistRemove(peer *PeerInfo) {
	peerlistMutex.Lock()
	defer peerlistMutex.Unlock()

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

// send sends a raw packet to the peer
func (peer *PeerInfo) send(packet *packetRaw) (err error) {
	if len(peer.Connections) == 0 {
		return errors.New("no valid connection to peer")
	}

	packet.Protocol = 0

	raw, err := packetEncrypt(peerPrivateKey, peer.PublicKey, packet)
	if err != nil {
		return err
	}

	atomic.AddUint64(&peer.StatsPacketSent, 1)

	// Send out the wire. Use connectionLatest if available.
	// Failover: If sending fails and there are other connections available, try those. Automatically update connectionLatest if one is successful.
	// Windows: This works great on if the adapter is disabled, however, does not detect if the network cable is unplugged.
	if peer.connectionLatest != nil {
		useConnection := peer.connectionLatest
		if err = useConnection.Network.send(useConnection.Address.IP, useConnection.Address.Port, raw); err != nil && len(peer.Connections) > 1 {
			err = peer.sendFailover(useConnection, raw)
		}
		return err
	}

	if err = peer.Connections[0].Network.send(peer.Connections[0].Address.IP, peer.Connections[0].Address.Port, raw); err != nil && len(peer.Connections) > 1 {
		err = peer.sendFailover(peer.Connections[0], raw)
	}
	return err
}

// sendFailover tries to send the packet over any other different (than the failed) connection.
// If send works, it will switch over connectionLatest.
// Windows: A common error when the network adapter is disabled is "wsasendto: The requested address is not valid in its context".
func (peer *PeerInfo) sendFailover(failed *Connection, raw []byte) (err error) {
	for _, connection := range peer.Connections {
		if connection != failed {
			err = connection.Network.send(connection.Address.IP, connection.Address.Port, raw)
			if err == nil {
				peer.connectionLatest = connection
				return nil
			}
		}
	}
	return err
}

// sendAllNetworks sends a raw packet via all networks
func sendAllNetworks(receiverPublicKey *btcec.PublicKey, packet *packetRaw, remote *net.UDPAddr) (err error) {
	packet.Protocol = 0
	raw, err := packetEncrypt(peerPrivateKey, receiverPublicKey, packet)
	if err != nil {
		return err
	}

	successCount := 0

	if IsIPv6(remote.IP.To16()) {
		for _, network := range networks6 {
			// Do not mix link-local unicast targets with non link-local networks (only when iface is known, i.e. not catch all local)
			if network.iface != nil && remote.IP.IsLinkLocalUnicast() != network.address.IP.IsLinkLocalUnicast() {
				continue
			}

			err = network.send(remote.IP, remote.Port, raw)
			if err == nil {
				successCount++
			}
		}
	} else {
		for _, network := range networks4 {
			// Do not mix link-local unicast targets with non link-local networks (only when iface is known, i.e. not catch all local)
			if network.iface != nil && remote.IP.IsLinkLocalUnicast() != network.address.IP.IsLinkLocalUnicast() {
				continue
			}

			err = network.send(remote.IP, remote.Port, raw)
			if err == nil {
				successCount++
			}
		}
	}

	if successCount == 0 {
		return errors.New("no successful send")
	}

	return nil
}

// registerConnection registers an incoming connection for an existing peer. If new, it will add to the list.
// Currently there is only a connectionLatest indicating the latest valid connection. In the future other priority such as last packet time, speed, local vs remote IP might be used.
func (peer *PeerInfo) registerConnection(remote *net.UDPAddr, network *Network) {
	for n := range peer.Connections {
		if peer.Connections[n].Address.IP.Equal(remote.IP) && peer.Connections[n].Network.address.IP.Equal(network.address.IP) {
			// Connection already established. Verify port and update if necessary.
			// Some NATs may rotate ports. Some mobile phone providers even rotate IPs which is not detected here.
			if peer.Connections[n].Address.Port != remote.Port {
				peer.Connections[n].Address.Port = remote.Port
			}

			peer.connectionLatest = peer.Connections[n]
			return
		}
	}

	peer.Connections = append(peer.Connections, &Connection{Address: remote, Network: network})

	peer.connectionLatest = peer.Connections[len(peer.Connections)-1]
}
