/*
File Name:  Filter.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Filters allow the caller to intercept events. The filter functions must not modify any data.
*/

package core

import (
	"log"

	"github.com/PeernetOfficial/core/dht"
	"github.com/PeernetOfficial/core/protocol"
	"github.com/btcsuite/btcd/btcec"
)

// Filters contains all functions to install the hook. Use nil for unused.
// The functions are called sequentially and block execution; if the filter takes a long time it should start a Go routine.
var Filters struct {
	// NewPeer is called every time a new peer, that is one that is not currently in the peer list.
	// Note that peers might be removed from peer lists and reappear quickly, i.e. this function may be called multiple times for the same peers.
	// The filter must maintain its own map of unique peer IDs if actual uniqueness of new peers is desired.
	NewPeer func(peer *PeerInfo, connection *Connection)

	// NewPeerConnection is called for each new established connection to a peer. Note that connections might be dropped and reconnected at anytime.
	NewPeerConnection func(peer *PeerInfo, connection *Connection)

	// LogError is called for any error. If this function is overwritten by the caller, the caller must write errors into the log file if desired, or call DefaultLogError.
	LogError func(function, format string, v ...interface{})

	// DHTSearchStatus is called with updates of searches in the DHT. It allows to see the live progress of searches.
	DHTSearchStatus func(client *dht.SearchClient, function, format string, v ...interface{})

	// IncomingRequest receives all incoming information requests. The action field is set accordingly.
	IncomingRequest func(peer *PeerInfo, Action int, Key []byte, Info interface{})

	// PacketIn is a low-level filter for incoming packets after they are decrypted.
	// Traverse messages are not covered.
	PacketIn func(packet *protocol.PacketRaw, senderPublicKey *btcec.PublicKey, connection *Connection)

	// PacketOut is a low-level filter for outgoing packets before they are encrypted.
	// IPv4 broadcast, IPv6 multicast, and Traverse messages are not covered.
	PacketOut func(packet *protocol.PacketRaw, receiverPublicKey *btcec.PublicKey, connection *Connection)

	// MessageIn is a high-level filter for decoded incoming messages. message is of type nil, MessageAnnouncement, MessageResponse, or MessageTraverse
	MessageIn func(peer *PeerInfo, raw *MessageRaw, message interface{})

	// MessageOutAnnouncement is a high-level filter for outgoing announcements. Peer is nil on first contact.
	// Broadcast and Multicast messages are not covered.
	MessageOutAnnouncement func(receiverPublicKey *btcec.PublicKey, peer *PeerInfo, packet *protocol.PacketRaw, findSelf bool, findPeer []KeyHash, findValue []KeyHash, files []InfoStore)

	// MessageOutResponse is a high-level filter for outgoing responses.
	MessageOutResponse func(peer *PeerInfo, packet *protocol.PacketRaw, hash2Peers []Hash2Peer, filesEmbed []EmbeddedFileData, hashesNotFound [][]byte)

	// MessageOutTraverse is a high-level filter for outgoing traverse messages.
	MessageOutTraverse func(peer *PeerInfo, packet *protocol.PacketRaw, embeddedPacket *protocol.PacketRaw, receiverEnd *btcec.PublicKey)

	// MessageOutPing is a high-level filter for outgoing pings.
	MessageOutPing func(peer *PeerInfo, packet *protocol.PacketRaw, connection *Connection)

	// MessageOutPong is a high-level filter for outgoing pongs.
	MessageOutPong func(peer *PeerInfo, packet *protocol.PacketRaw)
}

func initFilters() {
	// Set default filters to blank functions so they can be safely called without constant nil checks.
	// Only if not already set before init.

	if Filters.NewPeer == nil {
		Filters.NewPeer = func(peer *PeerInfo, connection *Connection) {}
	}
	if Filters.NewPeerConnection == nil {
		Filters.NewPeerConnection = func(peer *PeerInfo, connection *Connection) {}
	}
	if Filters.DHTSearchStatus == nil {
		Filters.DHTSearchStatus = func(client *dht.SearchClient, function, format string, v ...interface{}) {}
	}
	if Filters.LogError == nil {
		Filters.LogError = DefaultLogError
	}
	if Filters.IncomingRequest == nil {
		Filters.IncomingRequest = func(peer *PeerInfo, Action int, Key []byte, Info interface{}) {}
	}
	if Filters.PacketIn == nil {
		Filters.PacketIn = func(packet *protocol.PacketRaw, senderPublicKey *btcec.PublicKey, c *Connection) {}
	}
	if Filters.PacketOut == nil {
		Filters.PacketOut = func(packet *protocol.PacketRaw, receiverPublicKey *btcec.PublicKey, c *Connection) {}
	}
	if Filters.MessageIn == nil {
		Filters.MessageIn = func(peer *PeerInfo, raw *MessageRaw, message interface{}) {}
	}
	if Filters.MessageOutAnnouncement == nil {
		Filters.MessageOutAnnouncement = func(receiverPublicKey *btcec.PublicKey, peer *PeerInfo, packet *protocol.PacketRaw, findSelf bool, findPeer []KeyHash, findValue []KeyHash, files []InfoStore) {
		}
	}
	if Filters.MessageOutResponse == nil {
		Filters.MessageOutResponse = func(peer *PeerInfo, packet *protocol.PacketRaw, hash2Peers []Hash2Peer, filesEmbed []EmbeddedFileData, hashesNotFound [][]byte) {
		}
	}
	if Filters.MessageOutTraverse == nil {
		Filters.MessageOutTraverse = func(peer *PeerInfo, packet *protocol.PacketRaw, embeddedPacket *protocol.PacketRaw, receiverEnd *btcec.PublicKey) {
		}
	}
	if Filters.MessageOutPing == nil {
		Filters.MessageOutPing = func(peer *PeerInfo, packet *protocol.PacketRaw, connection *Connection) {}
	}
	if Filters.MessageOutPong == nil {
		Filters.MessageOutPong = func(peer *PeerInfo, packet *protocol.PacketRaw) {}
	}
}

// DefaultLogError is the default error logging function
func DefaultLogError(function, format string, v ...interface{}) {
	log.Printf("["+function+"] "+format, v...)
}
