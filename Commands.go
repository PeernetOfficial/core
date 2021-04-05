/*
File Name:  Commands.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"bytes"
	"fmt"
	"time"

	"github.com/PeernetOfficial/core/dht"
)

// respondClosesContactsCount is the number of closest contact to respond.
// Each peer record will take 55 bytes. Overhead is 73 + 15 payload header + UA length + 6 + 34 = 128 bytes without UA.
// It makes sense to stay below 508 bytes (no fragmentation). Reporting back 5 contacts for FIND_SELF requests should do the magic.
const respondClosesContactsCount = 5

// cmdAnouncement handles an incoming announcement
func (peer *PeerInfo) cmdAnouncement(msg *MessageAnnouncement) {
	added := false
	if peer == nil {
		// The added check is required due to potential race condition; initially the client may receive multiple incoming announcement from the same peer via different connections.
		if peer, added = PeerlistAdd(msg.SenderPublicKey, msg.connection); !added {
			return
		}

		fmt.Printf("Incoming initial announcement from %s\n", msg.connection.Address.String())
	} else {
		fmt.Printf("Incoming secondary announcement from %s\n", msg.connection.Address.String())
	}

	// Filter function to only share peers that are "connectable" to the remote one. It checks IPv4, IPv6, and local connection.
	filterFunc := func(allowLocal, allowIPv4, allowIPv6 bool) dht.NodeFilterFunc {
		return func(node *dht.Node) (accept bool) {
			return node.Info.(*PeerInfo).IsConnectable(allowLocal, allowIPv4, allowIPv6)
		}
	}

	allowIPv4 := msg.Features&(1<<FeatureIPv4Listen) > 0
	allowIPv6 := msg.Features&(1<<FeatureIPv6Listen) > 0

	var hash2Peers []Hash2Peer

	// FIND_SELF: Requesting peers close to the sender?
	if msg.Actions&(1<<ActionFindSelf) > 0 {
		// do not respond the caller's own peer (add to ignore list)
		for _, node := range nodesDHT.GetClosestContacts(respondClosesContactsCount, peer.NodeID, filterFunc(msg.connection.IsLocal(), allowIPv4, allowIPv6), peer.NodeID) {
			if info := node.Info.(*PeerInfo).peer2Info(msg.connection.IsLocal(), allowIPv4, allowIPv6); info != nil {
				hash2Peers = append(hash2Peers, Hash2Peer{ID: KeyHash{node.ID}, Closest: []InfoPeer{*info}})
			}
		}
	}

	// FIND_PEER: Find a different peer? Note that in this case no IPv4/IPv6 connectivity check is performed.
	if msg.Actions&(1<<ActionFindPeer) > 0 {
		// TODO
	}

	// Find a value?
	if msg.Actions&(1<<ActionFindValue) > 0 {
		// TODO
	}

	// Information about files stored by the sender?
	if msg.Actions&(1<<ActionInfoStore) > 0 {
		// TODO
	}

	// Empty announcement from existing peer means the peer most likely restarted. For regular connection upkeep ping should be used.
	peer.sendResponse(added, hash2Peers, nil, nil)
}

func (peer *PeerInfo) peer2Info(allowLocal, allowIPv4, allowIPv6 bool) (result *InfoPeer) {
	if connection := peer.GetConnection2Share(allowLocal, allowIPv4, allowIPv6); connection != nil {
		return &InfoPeer{
			PublicKey: peer.PublicKey,
			NodeID:    peer.NodeID,
			IP:        connection.Address.IP,
			Port:      uint16(connection.Address.Port),
		}
	}

	return nil
}

// cmdResponse handles the response to the announcement
func (peer *PeerInfo) cmdResponse(msg *MessageResponse) {
	// Future: We should only accept responses from peers that we contacted first for security reasons. This can be easily identified by Peer ID.

	if peer == nil {
		peer, _ = PeerlistAdd(msg.SenderPublicKey, msg.connection)
		fmt.Printf("Incoming initial response from %s\n", msg.connection.Address.String())
	}

	// check if incoming response to FIND_SELF
	for _, hash2peer := range msg.Hash2Peers {
		if !bytes.Equal(hash2peer.ID.Hash, nodeID) {
			for _, closePeer := range hash2peer.Closest {
				// Initiate contact. Once a response comes back, the peer is actually added to the list.
				contactArbitraryPeer(closePeer.PublicKey, closePeer.IP, closePeer.Port)
			}
		}
	}

	//fmt.Printf("Incoming response from %s on %s\n", msg.connection.Address.String(), msg.connection.Address.String())
}

// cmdPing handles an incoming ping message
func (peer *PeerInfo) cmdPing(msg *MessageRaw) {
	if peer == nil {
		// Unexpected incoming ping, reply with announce message
		// TODO
		return
	}
	peer.send(&PacketRaw{Command: CommandPong})
	//fmt.Printf("Incoming ping from %s on %s\n", msg.connection.Address.String(), msg.connection.Address.String())
}

// cmdPong handles an incoming pong message
func (peer *PeerInfo) cmdPong(msg *MessageRaw) {
	//fmt.Printf("Incoming pong from %s on %s\n", msg.connection.Address.String(), msg.connection.Address.String())
}

// cmdChat handles a chat message [debug]
func (peer *PeerInfo) cmdChat(msg *MessageRaw) {
	fmt.Printf("Chat from '%s': %s\n", msg.connection.Address.String(), string(msg.PacketRaw.Payload))
}

// pingTime is the time in seconds to send out ping messages
const pingTime = 10

// connectionInvalidate is the threshold in seconds to invalidate formerly active connections that no longer receive incoming packets.
const connectionInvalidate = 22

// connectionRemove is the threshold in seconds to remove inactive connections in case there is at least one active connection known.
const connectionRemove = 2 * 60

// autoPingAll sends out regular ping messages to all connections of all peers. This allows to detect invalid connections and eventually drop them.
func autoPingAll() {
	for {
		time.Sleep(time.Second)
		thresholdInvalidate1 := time.Now().Add(-connectionInvalidate * time.Second)
		thresholdInvalidate2 := time.Now().Add(-connectionInvalidate * time.Second * 4)
		thresholdPingOut1 := time.Now().Add(-pingTime * time.Second)
		thresholdPingOut2 := time.Now().Add(-pingTime * time.Second * 4)

		for _, peer := range PeerlistGet() {
			// first handle active connections
			for _, connection := range peer.GetConnections(true) {
				thresholdPing := thresholdPingOut1
				thresholdInv := thresholdInvalidate1

				if connection.Status == ConnectionRedundant {
					thresholdPing = thresholdPingOut2
					thresholdInv = thresholdInvalidate2
				}

				if connection.LastPacketIn.Before(thresholdInv) {
					peer.invalidateActiveConnection(connection)
					continue
				}

				if connection.LastPacketIn.Before(thresholdPing) && connection.LastPingOut.Before(thresholdPing) {
					peer.pingConnection(connection)
					continue
				}
			}

			// handle inactive connections
			for _, connection := range peer.GetConnections(false) {
				// If the inactive connection is expired, remove it; although only if there is at least one active connection, or two other inactive ones.
				if (len(peer.connectionActive) >= 1 || len(peer.connectionInactive) > 2) && connection.Expires.Before(time.Now()) {
					peer.removeInactiveConnection(connection)
					continue
				}

				// if no ping was sent recently, send one now
				if connection.LastPingOut.Before(thresholdPingOut1) {
					peer.pingConnection(connection)
				}
			}
		}
	}
}

// SendChatAll sends a text message to all peers
func SendChatAll(text string) {
	for _, peer := range PeerlistGet() {
		peer.Chat(text)
	}
}
