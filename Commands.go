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

// respondClosesContactsCount is the number of closest peers to return per request key.
// Each peer record takes 69 bytes. Reporting back 5 peers keeps a response to a single request well below the MTU.
const respondClosesContactsCount = 5

// announcementMaxKeys is the maximum number of keys answered per announcement across A_FIND_PEER, B_FIND_BLOCKCHAIN and B_FIND_FILE. Keys beyond that are declined (no response record).
const announcementMaxKeys = 20

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

	// closestPeers returns the closest peers to the key, excluding the sender.
	closestPeers := func(key []byte) (closest []protocol.PeerRecord) {
		for _, node := range peer.Backend.nodesDHT.GetClosestContacts(respondClosesContactsCount, key, filterFunc(connection.IsLocal(), allowIPv4, allowIPv6), peer.PublicKey) {
			if info := node.Info.(*PeerInfo).peer2Record(connection.IsLocal(), allowIPv4, allowIPv6); info != nil {
				closest = append(closest, *info)
			}
		}
		return closest
	}

	var records []protocol.ResponseRecord
	keyBudget := announcementMaxKeys

	// A_FIND_SELF: peers close to the sender
	if msg.FindSelf {
		peer.Backend.Filters.IncomingRequest(peer, protocol.RequestAFindSelf, peer.PublicKey, nil)

		records = append(records, protocol.ResponseRecord{Type: protocol.ResponseASelf, Closest: closestPeers(peer.PublicKey), IsLast: true})
	}

	// A_FIND_PEER: peers close to each key
	for _, key := range msg.FindPeer {
		if keyBudget == 0 {
			break
		}
		keyBudget--
		peer.Backend.Filters.IncomingRequest(peer, protocol.RequestAFindPeer, key, nil)

		records = append(records, protocol.ResponseRecord{Type: protocol.ResponseAPeer, Key: key, Closest: closestPeers(key), IsLast: true})
	}

	// B_FIND_BLOCKCHAIN: self if the blockchain is stored here (own or cached), and peers close to the key
	for _, key := range msg.FindBlockchain {
		if keyBudget == 0 {
			break
		}
		keyBudget--
		peer.Backend.Filters.IncomingRequest(peer, protocol.RequestBFindBlockchain, key, nil)

		record := protocol.ResponseRecord{Type: protocol.ResponseBBlockchain, Key: key, Closest: closestPeers(key), IsLast: true}
		if stored, height, version := peer.Backend.blockchainStored(key); stored {
			record.Storing = []protocol.StoringPeer{{PeerRecord: peer.Backend.selfPeerRecord(), BlockchainHeight: height, BlockchainVersion: version}}
		}
		records = append(records, record)
	}

	// B_FIND_FILE: self if the file is stored here, and peers close to the key
	for _, key := range msg.FindFile {
		if keyBudget == 0 {
			break
		}
		keyBudget--
		peer.Backend.Filters.IncomingRequest(peer, protocol.RequestBFindFile, key, nil)

		record := protocol.ResponseRecord{Type: protocol.ResponseBFile, Key: key, Closest: closestPeers(key), IsLast: true}
		if stored, _ := peer.announcementGetData(key); stored {
			record.Storing = []protocol.StoringPeer{{PeerRecord: peer.Backend.selfPeerRecord()}}
		}
		records = append(records, record)
	}

	// B_FIND_FILE_EMBED: the file embedded if stored here and it fits into the packet, otherwise as B_FIND_FILE
	for _, key := range msg.FindFileEmbed {
		if keyBudget == 0 {
			break
		}
		keyBudget--
		peer.Backend.Filters.IncomingRequest(peer, protocol.RequestBFindFileEmbed, key, nil)

		stored, data := peer.announcementGetData(key)
		if stored && len(data) > 0 {
			records = append(records, protocol.ResponseRecord{Type: protocol.ResponseBFileEmbed, Key: key, Data: data})
			continue
		}

		record := protocol.ResponseRecord{Type: protocol.ResponseBFile, Key: key, Closest: closestPeers(key), IsLast: true}
		if stored {
			record.Storing = []protocol.StoringPeer{{PeerRecord: peer.Backend.selfPeerRecord()}}
		}
		records = append(records, record)
	}

	// B_STORE_BLOCKCHAIN, B_STORE_FILE: listings of what other peers store are not kept. Store requests do not trigger a response.
	for n := range msg.StoreBlockchain {
		peer.Backend.Filters.IncomingRequest(peer, protocol.RequestBStoreBlockchain, msg.StoreBlockchain[n].PublicKey, &msg.StoreBlockchain[n])
	}
	for n := range msg.StoreFile {
		peer.Backend.Filters.IncomingRequest(peer, protocol.RequestBStoreFile, msg.StoreFile[n].Hash, &msg.StoreFile[n])
	}

	sendUA := msg.UserAgent != "" // Send user agent if one was provided. Per protocol the first announcement message must have the User Agent set.
	peer.sendResponse(msg.Sequence, sendUA, records)
}

func (peer *PeerInfo) peer2Record(allowLocal, allowIPv4, allowIPv6 bool) (result *protocol.PeerRecord) {
	connectionIPv4 := peer.GetConnection2Share(allowLocal, allowIPv4, false)
	connectionIPv6 := peer.GetConnection2Share(allowLocal, false, allowIPv6)
	if connectionIPv4 == nil && connectionIPv6 == nil {
		return nil
	}

	result = &protocol.PeerRecord{
		PublicKey: peer.PublicKey,
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
		if len(msg.Records) > 0 {
			peer.Backend.LogError("cmdResponse", "unsolicited response data received from %s\n", connection.Address.String())
		}

		return
	}

	// bootstrap A_FIND_SELF?
	if _, ok := msg.SequenceInfo.Data.(*bootstrapFindSelf); ok {
		for _, record := range msg.Records {
			// Make sure no garbage is returned. Only A_RESPONSE_SELF is expected.
			if record.Type != protocol.ResponseASelf {
				peer.Backend.LogError("cmdResponse", "incoming response to bootstrap A_FIND_SELF contains invalid data from %s\n", connection.Address.String())
				return
			}

			peer.cmdResponseBootstrapFindSelf(msg, record.Closest)
		}

		return
	}

	// Response to an information request?
	if info, ok := msg.SequenceInfo.Data.(*dht.InformationRequest); ok {
		// Future: Once multiple information requests are pooled (multiplexed) into one or multiple Announcement sequences (messages), the responses need to be de-pooled.
		// A simple multiplex structure linked via the sequence containing a map (hash 2 IR) could simplify this.
		for _, record := range msg.Records {
			// A_RESPONSE_SELF answers the own key; every other record carries the key.
			if record.Type != protocol.ResponseASelf && !bytes.Equal(record.Key, info.Key) || record.Type == protocol.ResponseASelf && !bytes.Equal(info.Key, peer.Backend.peerPublicKey) {
				continue
			}

			switch record.Type {
			case protocol.ResponseASelf, protocol.ResponseAPeer, protocol.ResponseBBlockchain, protocol.ResponseBFile:
				var storing []protocol.PeerRecord
				for n := range record.Storing {
					storing = append(storing, record.Storing[n].PeerRecord)
				}

				info.QueueResult(&dht.NodeMessage{SenderID: peer.PublicKey, Closest: peer.records2Nodes(record.Closest), Storing: peer.records2Nodes(storing)})

			case protocol.ResponseBFileEmbed:
				info.QueueResult(&dht.NodeMessage{SenderID: peer.PublicKey, Data: record.Data})

				info.Done()
				info.Terminate() // file was found, terminate the request.
				return
			}
		}

		// The node is done with the announcement once the last response message arrived. A request without a response record by then was declined.
		if msg.SequenceLast {
			info.Done()
		}
	}
}

// cmdPing handles an incoming ping message
func (peer *PeerInfo) cmdPing(msg *protocol.MessageRaw, connection *Connection) {
	// If PortInternal is 0, it means no incoming announcement or response message was received on that connection.
	// This means the ping is unexpected. In that case for security reasons the remote peer is not asked for FIND_SELF.
	if connection.PortInternal == 0 {
		peer.sendAnnouncement(true, nil, nil)
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
	fmt.Fprintf(peer.Backend.Stdout, "Chat from %s '%s': %s\n", hex.EncodeToString(peer.PublicKey), connection.Address.String(), string(msg.PacketRaw.Payload))
}

// cmdLocalDiscovery handles an incoming announcement via local discovery
func (peer *PeerInfo) cmdLocalDiscovery(msg *protocol.MessageAnnouncement, connection *Connection) {
	// 21.04.2021 update: Local peer discovery from public IPv4s is possible in datacenter situations. Keep it enabled for now.
	// only accept local discovery message from private IPs for IPv4
	// IPv6 DHCP routers typically assign public IPv6s and they can join multicast in the local network.
	//if connection.IsIPv4() && !connection.IsLocal() {
	//	LogError("cmdLocalDiscovery", "message received from non-local IP %s peer ID %s\n", connection.Address.String(), hex.EncodeToString(msg.SenderPublicKey))
	//	return
	//}

	peer.sendAnnouncement(true, &protocol.Requests{FindSelf: ShouldSendFindSelf()}, &bootstrapFindSelf{})
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
		if !msg.BlockchainPublicKey.Equal(peer.Backend.peerPublicKey) {
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
