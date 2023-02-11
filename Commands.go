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

	"github.com/PeernetOfficial/core/dht"
	"github.com/PeernetOfficial/core/protocol"
	"github.com/PeernetOfficial/core/warehouse"
	"github.com/google/uuid"
)

// respondClosesContactsCount is the number of closest contact to respond.
// Each peer record will take 70 bytes. Overhead is 77 + 20 payload header + UA length + 6 + 34 = 137 bytes without UA.
// It makes sense to stay below 508 bytes (no fragmentation). Reporting back 5 contacts for FIND_SELF requests should do the magic.
const respondClosesContactsCount = 5

// cmdAnouncement handles an incoming announcement. Connection may be nil for traverse relayed messages.
func (peer *PeerInfo) cmdAnouncement(msg *protocol.MessageAnnouncement, connection *Connection) {
	// Filter function to only share peers that are "connectable" to the remote one. It checks IPv4, IPv6, and local connection.
	filterFunc := func(allowLocal, allowIPv4, allowIPv6 bool) dht.NodeFilterFunc {
		return func(node *dht.Node) (accept bool) {
			return node.Info.(*PeerInfo).IsConnectable(allowLocal, allowIPv4, allowIPv6)
		}
	}

	allowIPv4 := msg.Features&(1<<protocol.FeatureIPv4Listen) > 0
	allowIPv6 := msg.Features&(1<<protocol.FeatureIPv6Listen) > 0

	var hash2Peers []protocol.Hash2Peer
	var hashesNotFound [][]byte
	var filesEmbed []protocol.EmbeddedFileData

	// FIND_SELF: Requesting peers close to the sender?
	if msg.Actions&(1<<protocol.ActionFindSelf) > 0 {
		peer.Backend.Filters.IncomingRequest(peer, protocol.ActionFindSelf, peer.NodeID, nil)

		selfD := protocol.Hash2Peer{ID: protocol.KeyHash{Hash: peer.NodeID}}

		// do not respond the caller's own peer (add to ignore list)
		for _, node := range peer.Backend.nodesDHT.GetClosestContacts(respondClosesContactsCount, peer.NodeID, filterFunc(connection.IsLocal(), allowIPv4, allowIPv6), peer.NodeID) {
			if info := node.Info.(*PeerInfo).peer2Record(connection.IsLocal(), allowIPv4, allowIPv6); info != nil {
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
	if msg.Actions&(1<<protocol.ActionFindPeer) > 0 && len(msg.FindPeerKeys) > 0 {
		for _, findPeer := range msg.FindPeerKeys {
			peer.Backend.Filters.IncomingRequest(peer, protocol.ActionFindPeer, findPeer.Hash, nil)

			details := protocol.Hash2Peer{ID: findPeer}

			// Same as before, put self as ignoredNodes.
			for _, node := range peer.Backend.nodesDHT.GetClosestContacts(respondClosesContactsCount, findPeer.Hash, filterFunc(connection.IsLocal(), allowIPv4, allowIPv6), peer.NodeID) {
				if info := node.Info.(*PeerInfo).peer2Record(connection.IsLocal(), allowIPv4, allowIPv6); info != nil {
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
	if msg.Actions&(1<<protocol.ActionFindValue) > 0 {
		for _, findHash := range msg.FindDataKeys {
			peer.Backend.Filters.IncomingRequest(peer, protocol.ActionFindValue, findHash.Hash, nil)

			stored, data := peer.announcementGetData(findHash.Hash)
			if stored && len(data) > 0 {
				filesEmbed = append(filesEmbed, protocol.EmbeddedFileData{ID: findHash, Data: data})
			} else if stored {
				selfRecord := peer.Backend.selfPeerRecord()
				hash2Peers = append(hash2Peers, protocol.Hash2Peer{ID: findHash, Storing: []protocol.PeerRecord{selfRecord}})
			} else {
				hashesNotFound = append(hashesNotFound, findHash.Hash)
			}
		}
	}

	// Information about files stored by the sender?
	if msg.Actions&(1<<protocol.ActionInfoStore) > 0 && len(msg.InfoStoreFiles) > 0 {
		for n := range msg.InfoStoreFiles {
			peer.Backend.Filters.IncomingRequest(peer, protocol.ActionInfoStore, msg.InfoStoreFiles[n].ID.Hash, &msg.InfoStoreFiles[n])
		}

		peer.announcementStore(msg.InfoStoreFiles)
	}

	sendUA := msg.UserAgent != "" // Send user agent if one was provided. Per protocol the first announcement message must have the User Agent set.
	peer.sendResponse(msg.Sequence, sendUA, hash2Peers, filesEmbed, hashesNotFound)
}

func (peer *PeerInfo) peer2Record(allowLocal, allowIPv4, allowIPv6 bool) (result *protocol.PeerRecord) {
	connectionIPv4 := peer.GetConnection2Share(allowLocal, allowIPv4, false)
	connectionIPv6 := peer.GetConnection2Share(allowLocal, false, allowIPv6)
	if connectionIPv4 == nil && connectionIPv6 == nil {
		return nil
	}

	result = &protocol.PeerRecord{
		PublicKey: peer.PublicKey,
		NodeID:    peer.NodeID,
		Features:  peer.Features,
	}

	if connectionIPv4 != nil {
		result.IPv4 = connectionIPv4.Address.IP
		result.IPv4Port = uint16(connectionIPv4.Address.Port)
		result.IPv4PortReportedInternal = connectionIPv4.PortInternal
		result.IPv4PortReportedExternal = connectionIPv4.PortExternal
	}

	if connectionIPv6 != nil {
		result.IPv6 = connectionIPv6.Address.IP
		result.IPv6Port = uint16(connectionIPv6.Address.Port)
		result.IPv6PortReportedInternal = connectionIPv6.PortInternal
		result.IPv6PortReportedExternal = connectionIPv6.PortExternal
	}

	return result
}

// cmdResponse handles the response to the announcement
func (peer *PeerInfo) cmdResponse(msg *protocol.MessageResponse, connection *Connection) {
	// The sequence data is used to correlate this response with the announcement.
	if msg.SequenceInfo == nil || msg.SequenceInfo.Data == nil {
		// If there is no sequence data but there were results returned, it means we received unsolicited response data. It will be rejected.
		if len(msg.HashesNotFound) > 0 || len(msg.Hash2Peers) > 0 || len(msg.FilesEmbed) > 0 {
			peer.Backend.LogError("cmdResponse", "unsolicited response data received from %s\n", connection.Address.String())
		}

		return
	}

	// bootstrap FIND_SELF?
	if _, ok := msg.SequenceInfo.Data.(*bootstrapFindSelf); ok {
		for _, hash2Peer := range msg.Hash2Peers {
			// Make sure no garbage is returned. The key must be self and only Closest is expected.
			if !bytes.Equal(hash2Peer.ID.Hash, peer.Backend.nodeID) || len(hash2Peer.Closest) == 0 {
				peer.Backend.LogError("cmdResponse", "incoming response to bootstrap FIND_SELF contains invalid data from %s\n", connection.Address.String())
				return
			}

			peer.cmdResponseBootstrapFindSelf(msg, hash2Peer.Closest)
		}

		return
	}

	// Response to an information request?
	if _, ok := msg.SequenceInfo.Data.(*dht.InformationRequest); ok {
		// Future: Once multiple information requests are pooled (multiplexed) into one or multiple Announcement sequences (messages), the responses need to be de-pooled.
		// A simple multiplex structure linked via the sequence containing a map (hash 2 IR) could simplify this.
		info := msg.SequenceInfo.Data.(*dht.InformationRequest)

		if len(msg.HashesNotFound) > 0 {
			info.Done()
		}

		for _, hash2Peer := range msg.Hash2Peers {
			info.QueueResult(&dht.NodeMessage{SenderID: peer.NodeID, Closest: peer.records2Nodes(hash2Peer.Closest), Storing: peer.records2Nodes(hash2Peer.Storing)})

			if hash2Peer.IsLast {
				info.Done()
			}
		}

		for _, file := range msg.FilesEmbed {
			info.QueueResult(&dht.NodeMessage{SenderID: peer.NodeID, Data: file.Data})

			info.Done()
			info.Terminate() // file was found, terminate the request.
		}
	}
}

// cmdPing handles an incoming ping message
func (peer *PeerInfo) cmdPing(msg *protocol.MessageRaw, connection *Connection) {
	// If PortInternal is 0, it means no incoming announcement or response message was received on that connection.
	// This means the ping is unexpected. In that case for security reasons the remote peer is not asked for FIND_SELF.
	if connection.PortInternal == 0 {
		peer.sendAnnouncement(true, false, nil, nil, nil, nil)
		return
	}

	raw := &protocol.PacketRaw{Command: protocol.CommandPong, Sequence: msg.Sequence}

	peer.Backend.Filters.MessageOutPong(peer, raw)

	peer.send(raw)
}

// cmdPong handles an incoming pong message
func (peer *PeerInfo) cmdPong(msg *protocol.MessageRaw, connection *Connection) {
}

// cmdChat handles a chat message [debug]
func (peer *PeerInfo) cmdChat(msg *protocol.MessageRaw, connection *Connection) {
	fmt.Fprintf(peer.Backend.Stdout, "Chat from %s '%s': %s\n", hex.EncodeToString(peer.PublicKey.SerializeCompressed()), connection.Address.String(), string(msg.PacketRaw.Payload))
}

// cmdLocalDiscovery handles an incoming announcement via local discovery
func (peer *PeerInfo) cmdLocalDiscovery(msg *protocol.MessageAnnouncement, connection *Connection) {
	// 21.04.2021 update: Local peer discovery from public IPv4s is possible in datacenter situations. Keep it enabled for now.
	// only accept local discovery message from private IPs for IPv4
	// IPv6 DHCP routers typically assign public IPv6s and they can join multicast in the local network.
	//if connection.IsIPv4() && !connection.IsLocal() {
	//	LogError("cmdLocalDiscovery", "message received from non-local IP %s peer ID %s\n", connection.Address.String(), hex.EncodeToString(msg.SenderPublicKey.SerializeCompressed()))
	//	return
	//}

	peer.sendAnnouncement(true, ShouldSendFindSelf(), nil, nil, nil, &bootstrapFindSelf{})
}

// SendChatAll sends a text message to all peers
func (backend *Backend) SendChatAll(text string) {
	for _, peer := range backend.PeerlistGet() {
		peer.Chat(text)
	}
}

// cmdTransfer handles an incoming transfer message
func (peer *PeerInfo) cmdTransfer(msg *protocol.MessageTransfer, connection *Connection) {
	// Only UDT protocol is currently supported for file transfer.
	if msg.TransferProtocol != protocol.TransferProtocolUDT {
		return
	}

	switch msg.Control {
	case protocol.TransferControlRequestStart:
		// First check if the file available in the warehouse.
		_, fileSize, status, _ := peer.Backend.UserWarehouse.FileExists(msg.Hash)
		if status != warehouse.StatusOK {
			// File not available.
			peer.sendTransfer(nil, protocol.TransferControlNotAvailable, msg.TransferProtocol, msg.Hash, 0, 0, msg.Sequence, uuid.UUID{}, false)
			return
		} else if msg.Limit > 0 && fileSize < msg.Offset+msg.Limit {
			// If the read limit is out of bounds, this request is considered invalid and silently discarded.
			return
		}

		// Create a local UDT client to connect to the remote UDT server and serve the file!
		go peer.startFileTransferUDT(msg.Hash, fileSize, msg.Offset, msg.Limit, msg.Sequence, msg.TransferID, msg.TransferProtocol)

	case protocol.TransferControlActive:
		if v, ok := msg.SequenceInfo.Data.(*VirtualPacketConn); ok {
			go v.receiveData(msg.Data)
			return
		}

	case protocol.TransferControlNotAvailable:
		if v, ok := msg.SequenceInfo.Data.(*VirtualPacketConn); ok {
			v.Terminate(404)
			return
		}

	case protocol.TransferControlTerminate:
		if v, ok := msg.SequenceInfo.Data.(*VirtualPacketConn); ok {
			v.Terminate(2)
			return
		}

	}
}

// cmdGetBlock handles an incoming block message
func (peer *PeerInfo) cmdGetBlock(msg *protocol.MessageGetBlock, connection *Connection) {
	switch msg.Control {
	case protocol.GetBlockControlRequestStart:
		// Currently only support the local blockchain.
		if !msg.BlockchainPublicKey.IsEqual(peer.Backend.PeerPublicKey) {
			peer.sendGetBlock(nil, protocol.GetBlockControlNotAvailable, msg.BlockchainPublicKey, 0, 0, nil, msg.Sequence, uuid.UUID{}, false)
			return
		} else if _, height, _ := peer.Backend.UserBlockchain.Header(); height == 0 {
			peer.sendGetBlock(nil, protocol.GetBlockControlEmpty, msg.BlockchainPublicKey, 0, 0, nil, msg.Sequence, uuid.UUID{}, false)
			return
		} else if msg.LimitBlockCount == 0 {
			peer.sendGetBlock(nil, protocol.GetBlockControlTerminate, msg.BlockchainPublicKey, 0, 0, nil, msg.Sequence, uuid.UUID{}, false)
			return
		}

		// Create a local UDT client to connect to the remote UDT server and serve the blocks!
		go peer.startBlockTransfer(msg.BlockchainPublicKey, msg.LimitBlockCount, msg.MaxBlockSize, msg.TargetBlocks, msg.Sequence, msg.TransferID)

	case protocol.GetBlockControlActive:
		if v, ok := msg.SequenceInfo.Data.(*VirtualPacketConn); ok {
			go v.receiveData(msg.Data)
			return
		}

	case protocol.GetBlockControlNotAvailable:
		if v, ok := msg.SequenceInfo.Data.(*VirtualPacketConn); ok {
			v.Terminate(404)
			return
		}

	case protocol.GetBlockControlEmpty:
		if v, ok := msg.SequenceInfo.Data.(*VirtualPacketConn); ok {
			v.Terminate(410)
			return
		}

	case protocol.GetBlockControlTerminate:
		if v, ok := msg.SequenceInfo.Data.(*VirtualPacketConn); ok {
			v.Terminate(2)
			return
		}

	}
}
