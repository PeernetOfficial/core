/*
File Name:  Filter.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Filters allow the caller to intercept events. The filter functions must not modify any data.
*/

package core

import (
	"io"
	"sync"

	"github.com/PeernetOfficial/core/blockchain"
	"github.com/PeernetOfficial/core/btcec"
	"github.com/PeernetOfficial/core/dht"
	"github.com/PeernetOfficial/core/protocol"
	"github.com/google/uuid"
)

// Filters contains all functions to install the hook. Use nil for unused.
// The functions are called sequentially and block execution; if the filter takes a long time it should start a Go routine.
type Filters struct {
	// NewPeer is called every time a new peer, that is one that is not currently in the peer list.
	// Note that peers might be removed from peer lists and reappear quickly, i.e. this function may be called multiple times for the same peers.
	// The filter must maintain its own map of unique peer IDs if actual uniqueness of new peers is desired.
	NewPeer func(peer *PeerInfo, connection *Connection)

	// NewPeerConnection is called for each new established connection to a peer. Note that connections might be dropped and reconnected at anytime.
	NewPeerConnection func(peer *PeerInfo, connection *Connection)

	// LogError is called for any error.
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
	MessageIn func(peer *PeerInfo, raw *protocol.MessageRaw, message interface{})

	// MessageOutAnnouncement is a high-level filter for outgoing announcements. Peer is nil on first contact.
	// Broadcast and Multicast messages are not covered.
	MessageOutAnnouncement func(receiverPublicKey *btcec.PublicKey, peer *PeerInfo, packet *protocol.PacketRaw, findSelf bool, findPeer []protocol.KeyHash, findValue []protocol.KeyHash, files []protocol.InfoStore)

	// MessageOutResponse is a high-level filter for outgoing responses.
	MessageOutResponse func(peer *PeerInfo, packet *protocol.PacketRaw, hash2Peers []protocol.Hash2Peer, filesEmbed []protocol.EmbeddedFileData, hashesNotFound [][]byte)

	// MessageOutTraverse is a high-level filter for outgoing traverse messages.
	MessageOutTraverse func(peer *PeerInfo, packet *protocol.PacketRaw, embeddedPacket *protocol.PacketRaw, receiverEnd *btcec.PublicKey)

	// MessageOutPing is a high-level filter for outgoing pings.
	MessageOutPing func(peer *PeerInfo, packet *protocol.PacketRaw, connection *Connection)

	// MessageOutPong is a high-level filter for outgoing pongs.
	MessageOutPong func(peer *PeerInfo, packet *protocol.PacketRaw)

	// Called when the statistics change of a single blockchain in the cache. Must be set on init.
	GlobalBlockchainCacheStatistic func(multi *blockchain.MultiStore, header *blockchain.MultiBlockchainHeader, statsOld blockchain.BlockchainStats)

	// Called after a blockchain is deleted from the blockchain cache. The header reflects the status before deletion. Must be set on init.
	GlobalBlockchainCacheDelete func(multi *blockchain.MultiStore, header *blockchain.MultiBlockchainHeader)
}

func (backend *Backend) initFilters() {
	// Set default filters to blank functions so they can be safely called without constant nil checks.
	// Only if not already set before init.

	if backend.Filters.NewPeer == nil {
		backend.Filters.NewPeer = func(peer *PeerInfo, connection *Connection) {}
	}
	if backend.Filters.NewPeerConnection == nil {
		backend.Filters.NewPeerConnection = func(peer *PeerInfo, connection *Connection) {}
	}
	if backend.Filters.DHTSearchStatus == nil {
		backend.Filters.DHTSearchStatus = func(client *dht.SearchClient, function, format string, v ...interface{}) {}
	}
	if backend.Filters.LogError == nil {
		backend.Filters.LogError = func(function, format string, v ...interface{}) {}
	}
	if backend.Filters.IncomingRequest == nil {
		backend.Filters.IncomingRequest = func(peer *PeerInfo, Action int, Key []byte, Info interface{}) {}
	}
	if backend.Filters.PacketIn == nil {
		backend.Filters.PacketIn = func(packet *protocol.PacketRaw, senderPublicKey *btcec.PublicKey, c *Connection) {}
	}
	if backend.Filters.PacketOut == nil {
		backend.Filters.PacketOut = func(packet *protocol.PacketRaw, receiverPublicKey *btcec.PublicKey, c *Connection) {}
	}
	if backend.Filters.MessageIn == nil {
		backend.Filters.MessageIn = func(peer *PeerInfo, raw *protocol.MessageRaw, message interface{}) {}
	}
	if backend.Filters.MessageOutAnnouncement == nil {
		backend.Filters.MessageOutAnnouncement = func(receiverPublicKey *btcec.PublicKey, peer *PeerInfo, packet *protocol.PacketRaw, findSelf bool, findPeer []protocol.KeyHash, findValue []protocol.KeyHash, files []protocol.InfoStore) {
		}
	}
	if backend.Filters.MessageOutResponse == nil {
		backend.Filters.MessageOutResponse = func(peer *PeerInfo, packet *protocol.PacketRaw, hash2Peers []protocol.Hash2Peer, filesEmbed []protocol.EmbeddedFileData, hashesNotFound [][]byte) {
		}
	}
	if backend.Filters.MessageOutTraverse == nil {
		backend.Filters.MessageOutTraverse = func(peer *PeerInfo, packet *protocol.PacketRaw, embeddedPacket *protocol.PacketRaw, receiverEnd *btcec.PublicKey) {
		}
	}
	if backend.Filters.MessageOutPing == nil {
		backend.Filters.MessageOutPing = func(peer *PeerInfo, packet *protocol.PacketRaw, connection *Connection) {}
	}
	if backend.Filters.MessageOutPong == nil {
		backend.Filters.MessageOutPong = func(peer *PeerInfo, packet *protocol.PacketRaw) {}
	}
}

// MultiWriter code that allows to subscribe/unsubscribe.
type multiWriter struct {
	writers map[uuid.UUID]io.Writer
	sync.Mutex
}

// Creates a new writer that duplicates its writes to all the subscribed writers.
// Each write is written to each subscribed writer, one at a time. If any writer returns an error, the entire write operation continues.
func newMultiWriter() *multiWriter {
	return &multiWriter{writers: make(map[uuid.UUID]io.Writer)}
}

// Subscribe a new writer to the list of writers
func (m *multiWriter) Subscribe(writer io.Writer) (id uuid.UUID) {
	m.Lock()
	defer m.Unlock()

	id = uuid.New()
	m.writers[id] = writer

	return id
}

// Unsubscribe a writer from the list of writers
func (m *multiWriter) Unsubscribe(id uuid.UUID) {
	m.Lock()
	defer m.Unlock()

	delete(m.writers, id)
}

// Write a slice of byte to each of the subscribed writers. It will not return any errors.
func (m *multiWriter) Write(p []byte) (n int, err error) {
	m.Lock()
	defer m.Unlock()

	for _, w := range m.writers {
		w.Write(p)
	}
	return len(p), nil
}
