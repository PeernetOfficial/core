/*
File Name:  Commands.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"fmt"
	"time"

	"github.com/btcsuite/btcd/btcec"
)

// Commands between peers
const (
	// Peer List Management
	CommandAnnouncement = 0 // Announcement
	CommandResponse     = 1 // Response
	CommandPing         = 2 // Keep-alive message (no payload).
	CommandPong         = 3 // Response to ping (no payload).

	// Blockchain
	CommandGet = 4 // Request blocks for specified peer.

	// File Discovery

	// Debug
	CommandChat = 10 // Chat message [debug]
)

// packet2 is a high-level message between peers
type packet2 struct {
	PacketRaw
	SenderPublicKey *btcec.PublicKey // Sender Public Key, ECDSA (secp256k1) 257-bit
	connection      *Connection      // Connection that received the packet
}

// cmdAnouncement handles an incoming announcement
func (peer *PeerInfo) cmdAnouncement(msg *packet2) {
	if peer == nil {
		peer, added := PeerlistAdd(msg.SenderPublicKey, msg.connection)
		fmt.Printf("Incoming initial announcement from %s\n", msg.connection.Address.String())

		// send the Response
		if added {
			peer.send(&PacketRaw{Command: CommandResponse})
		}

		return
	}
	fmt.Printf("Incoming secondary announcement from %s\n", msg.connection.Address.String())

	// Announcement from existing peer means the peer most likely restarted
	peer.send(&PacketRaw{Command: CommandResponse})
}

// cmdResponse handles the response to the announcement
func (peer *PeerInfo) cmdResponse(msg *packet2) {
	if peer == nil {
		peer, _ = PeerlistAdd(msg.SenderPublicKey, msg.connection)
		fmt.Printf("Incoming initial response from %s\n", msg.connection.Address.String())

		return
	}

	fmt.Printf("Incoming response from %s on %s\n", msg.connection.Address.String(), msg.connection.Address.String())
}

// cmdPing handles an incoming ping message
func (peer *PeerInfo) cmdPing(msg *packet2) {
	peer.send(&PacketRaw{Command: CommandPong})
	//fmt.Printf("Incoming ping from %s on %s\n", msg.connection.Address.String(), msg.connection.Address.String())
}

// cmdPong handles an incoming pong message
func (peer *PeerInfo) cmdPong(msg *packet2) {
	//fmt.Printf("Incoming pong from %s on %s\n", msg.connection.Address.String(), msg.connection.Address.String())
}

// cmdChat handles a chat message [debug]
func (peer *PeerInfo) cmdChat(msg *packet2) {
	fmt.Printf("Chat from '%s': %s\n", msg.connection.Address.String(), string(msg.PacketRaw.Payload))
}

// pingTime is the time in seconds to send out ping messages
const pingTime = 10

// connectionInvalidate is the threshold in seconds to invalidate formerly active connections that no longer receive incoming packets.
const connectionInvalidate = 20

// connectionRemove is the threshold in seconds to remove inactive connections in case there is at least one active connection known.
const connectionRemove = 2 * 60

// autoPingAll sends out regular ping messages to all connections of all peers. This allows to detect invalid connections and eventually drop them.
func autoPingAll() {
	for {
		time.Sleep(time.Second)
		thresholdInvalidate := time.Now().Add(-connectionInvalidate * time.Second)
		thresholdRemove := time.Now().Add(-(connectionRemove + connectionInvalidate) * time.Second)
		thresholdPingOut := time.Now().Add(-pingTime * time.Second)

		for _, peer := range PeerlistGet() {
			// first handle active connections
			for _, connection := range peer.GetConnections(true) {
				// Check if no incoming packet for the last X seconds. Regularly sent pings should result in incoming packets.
				if connection.LastPacketIn.Before(thresholdInvalidate) {
					peer.invalidateActiveConnection(connection)
				}

				// Send ping if none was sent recently and no incoming packet was received recently.
				if connection.LastPacketIn.Before(thresholdPingOut) && connection.LastPingOut.Before(thresholdPingOut) {
					peer.sendConnection(&PacketRaw{Command: CommandPing}, connection)
					connection.LastPingOut = time.Now()
				}
			}

			// handle inactive connections
			for _, connection := range peer.GetConnections(false) {
				// Remove connections that have been inactive for a long time; only if there is at least an active connection, or at least two other inactive ones.
				if connection.LastPacketIn.Before(thresholdRemove) && (len(peer.connectionActive) >= 1 || len(peer.connectionInactive) > 2) {
					peer.removeInactiveConnection(connection)
					continue
				}

				// if no ping was sent recently, send one now
				if connection.LastPingOut.Before(thresholdPingOut) {
					peer.sendConnection(&PacketRaw{Command: CommandPing}, connection)
					connection.LastPingOut = time.Now()
				}
			}
		}
	}
}

// SendChatAll sends a text message to all peers
func SendChatAll(text string) {
	for _, peer := range PeerlistGet() {
		peer.send(&PacketRaw{Command: CommandChat, Payload: []byte(text)})
	}
}
