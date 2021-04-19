/*
File Name:  Commands.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"github.com/PeernetOfficial/core/dht"
)

// respondClosesContactsCount is the number of closest contact to respond.
// Each peer record will take 55 bytes. Overhead is 77 + 15 payload header + UA length + 6 + 34 = 132 bytes without UA.
// It makes sense to stay below 508 bytes (no fragmentation). Reporting back 5 contacts for FIND_SELF requests should do the magic.
const respondClosesContactsCount = 5

// cmdAnouncement handles an incoming announcement
func (peer *PeerInfo) cmdAnouncement(msg *MessageAnnouncement) {
	var added bool
	if peer == nil {
		peer, added = PeerlistAdd(msg.SenderPublicKey, msg.connection)
		fmt.Printf("Incoming initial announcement from %s\n", msg.connection.Address.String())
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
	var hashesNotFound [][]byte
	var filesEmbed []EmbeddedFileData

	// FIND_SELF: Requesting peers close to the sender?
	if msg.Actions&(1<<ActionFindSelf) > 0 {
		selfD := Hash2Peer{ID: KeyHash{peer.NodeID}}

		// do not respond the caller's own peer (add to ignore list)
		for _, node := range nodesDHT.GetClosestContacts(respondClosesContactsCount, peer.NodeID, filterFunc(msg.connection.IsLocal(), allowIPv4, allowIPv6), peer.NodeID) {
			if info := node.Info.(*PeerInfo).peer2Record(msg.connection.IsLocal(), allowIPv4, allowIPv6); info != nil {
				selfD.Closest = append(selfD.Closest, *info)
			}
		}

		if len(selfD.Closest) > 0 {
			hash2Peers = append(hash2Peers, selfD)
		} else {
			hashesNotFound = append(hashesNotFound, peer.NodeID)
		}
	}

	// FIND_PEER: Find a different peer?
	if msg.Actions&(1<<ActionFindPeer) > 0 && len(msg.FindPeerKeys) > 0 {
		for _, findPeer := range msg.FindPeerKeys {
			details := Hash2Peer{ID: findPeer}

			for _, node := range nodesDHT.GetClosestContacts(respondClosesContactsCount, findPeer.Hash, filterFunc(msg.connection.IsLocal(), allowIPv4, allowIPv6)) {
				if info := node.Info.(*PeerInfo).peer2Record(msg.connection.IsLocal(), allowIPv4, allowIPv6); info != nil {
					details.Closest = append(details.Closest, *info)
				}
			}

			if len(details.Closest) > 0 {
				hash2Peers = append(hash2Peers, details)
			} else {
				hashesNotFound = append(hashesNotFound, findPeer.Hash)
			}
		}
	}

	// Find a value?
	if msg.Actions&(1<<ActionFindValue) > 0 {
		for _, findHash := range msg.FindDataKeys {
			stored, data := announcementGetData(findHash.Hash)
			if stored && len(data) > 0 {
				filesEmbed = append(filesEmbed, EmbeddedFileData{ID: findHash, Data: data})
			} else if stored {
				selfRecord := selfPeerRecord(msg.connection.Network)
				hash2Peers = append(hash2Peers, Hash2Peer{ID: findHash, Storing: []PeerRecord{selfRecord}})
			} else {
				hashesNotFound = append(hashesNotFound, findHash.Hash)
			}
		}
	}

	// Information about files stored by the sender?
	if msg.Actions&(1<<ActionInfoStore) > 0 && len(msg.InfoStoreFiles) > 0 {
		peer.announcementStore(msg.InfoStoreFiles)
	}

	peer.sendResponse(msg.Sequence, added, hash2Peers, filesEmbed, hashesNotFound)
}

func (peer *PeerInfo) peer2Record(allowLocal, allowIPv4, allowIPv6 bool) (result *PeerRecord) {
	if connection := peer.GetConnection2Share(allowLocal, allowIPv4, allowIPv6); connection != nil {
		return &PeerRecord{
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
	if peer == nil {
		peer, _ = PeerlistAdd(msg.SenderPublicKey, msg.connection)
		fmt.Printf("Incoming initial response from %s\n", msg.connection.Address.String())
	}

	// Each result is checked against the list of information requests. Only 1 response by peer is permitted per query currently.
	// Response packets could be duplicated, which could happen due to auto broadcast when the remote peer responds and has multiple connections to self but none was active.

	for _, hash := range msg.HashesNotFound {
		info := nodesDHT.IRLookup(peer.NodeID, hash)
		if info == nil {
			continue
		}
		info.ActiveNodesSub(1)
	}

	for _, hash2Peer := range msg.Hash2Peers {
		info := nodesDHT.IRLookup(peer.NodeID, hash2Peer.ID.Hash)
		if info == nil {
			// Response to FIND_SELF?
			if bytes.Equal(hash2Peer.ID.Hash, nodeID) && len(hash2Peer.Closest) > 0 {
				peer.cmdResponseFindSelf(msg, hash2Peer.Closest)
			}
			continue
		}

		info.ResultChan <- &dht.NodeMessage{SenderID: peer.NodeID, Closest: records2Nodes(hash2Peer.Closest, msg.connection.Network), Storing: records2Nodes(hash2Peer.Storing, msg.connection.Network)}

		// Future: a terminate field inform wether the remote peer is done sending. For now terminate after 1 packet.
		info.ActiveNodesSub(1)
	}

	for _, file := range msg.FilesEmbed {
		info := nodesDHT.IRLookup(peer.NodeID, file.ID.Hash)
		if info == nil {
			continue
		}

		info.ResultChan <- &dht.NodeMessage{SenderID: peer.NodeID, Data: file.Data}

		info.ActiveNodesSub(1)
		info.Terminate() // file was found, terminate the request.
	}
}

// cmdPing handles an incoming ping message
func (peer *PeerInfo) cmdPing(msg *MessageRaw) {
	if peer == nil {
		// Unexpected incoming ping, reply with announce message
		peer, _ = PeerlistAdd(msg.SenderPublicKey, msg.connection)
		peer.sendAnnouncement(true, true, nil, nil, nil)
	}
	peer.send(&PacketRaw{Command: CommandPong, Sequence: msg.Sequence})
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

// cmdLocalDiscovery handles an incoming announcement via local discovery
func (peer *PeerInfo) cmdLocalDiscovery(msg *MessageAnnouncement) {
	// only accept local discovery message from private IPs for IPv4
	// IPv6 DHCP routers typically assign public IPv6s and they can join multicast in the local network.
	if msg.connection.IsIPv4() && !msg.connection.IsLocal() {
		log.Printf("cmdLocalDiscovery message received from non-local IP %s peer ID %s\n", msg.connection.Address.String(), hex.EncodeToString(msg.SenderPublicKey.SerializeCompressed()))
		return
	}

	if peer == nil {
		peer, _ = PeerlistAdd(msg.SenderPublicKey, msg.connection)

		fmt.Printf("Incoming initial local discovery from %s\n", msg.connection.Address.String())
		//} else {
		//	fmt.Printf("Incoming secondary local discovery from %s\n", msg.connection.Address.String())
	}

	peer.sendAnnouncement(true, true, nil, nil, nil)
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
