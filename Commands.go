/*
File Name:  Commands.go
Copyright:  2021 Kleissner Ventures Ltd
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
// A peer record with one IPv4 and one IPv6 address takes 66 bytes. Reporting back 5 peers keeps a response to a single request well below the MTU.
const respondClosesContactsCount = 5

// Maximum count of addresses per family in a peer record, by the response record that carries it.
const (
	respondAddressesMaxDHTA = protocol.PeerRecordAddressesMax // A_RESPONSE_SELF, A_RESPONSE_PEER
	respondAddressesMaxDHTB = 1                               // B_RESPONSE_BLOCKCHAIN, B_RESPONSE_FILE
)

// respondPeerRecordsSizeTarget is the target size in bytes of the peer records of the closest peers per request key.
// Each peer keeps one address per family; further addresses are only reported while the target size holds. 5 peer records with one IPv4 and one IPv6 address each fit.
// The value is what a packet of 508 bytes (UDP minimum no fragmentation) leaves for the peer records of an A_RESPONSE_PEER record:
// 508 - packet 108 - message header 23 - record count 1 - record header 3 - key 32 - peer record count 1.
// A response that carries the User Agent or a storing peer record is larger.
const respondPeerRecordsSizeTarget = 340

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

	// closestPeers returns the closest peers to the key, excluding the sender. addressesMax is the maximum count of addresses per family in each peer record.
	closestPeers := func(key []byte, addressesMax int) (closest []protocol.PeerRecord) {
		for _, node := range peer.Backend.nodesDHT.GetClosestContacts(respondClosesContactsCount, key, filterFunc(connection.IsLocal(), allowIPv4, allowIPv6), peer.PublicKey) {
			if info := node.Info.(*PeerInfo).peer2Record(connection.IsLocal(), allowIPv4, allowIPv6, addressesMax); info != nil {
				closest = append(closest, *info)
			}
		}
		limitPeerRecordsSize(closest, respondPeerRecordsSizeTarget)
		return closest
	}

	var records []protocol.ResponseRecord
	keyBudget := announcementMaxKeys

	// A_FIND_SELF: peers close to the sender
	if msg.FindSelf {
		peer.Backend.Filters.IncomingRequest(peer, protocol.RequestAFindSelf, peer.PublicKey, nil)

		records = append(records, protocol.ResponseRecord{Type: protocol.ResponseASelf, Closest: closestPeers(peer.PublicKey, respondAddressesMaxDHTA), IsLast: true})
	}

	// A_FIND_PEER: peers close to each key
	for _, key := range msg.FindPeer {
		if keyBudget == 0 {
			break
		}
		keyBudget--
		peer.Backend.Filters.IncomingRequest(peer, protocol.RequestAFindPeer, key, nil)

		records = append(records, protocol.ResponseRecord{Type: protocol.ResponseAPeer, Key: key, Closest: closestPeers(key, respondAddressesMaxDHTA), IsLast: true})
	}

	// B_FIND_BLOCKCHAIN: self if the blockchain is stored here (own or cached), and peers close to the key
	for _, key := range msg.FindBlockchain {
		if keyBudget == 0 {
			break
		}
		keyBudget--
		peer.Backend.Filters.IncomingRequest(peer, protocol.RequestBFindBlockchain, key, nil)

		record := protocol.ResponseRecord{Type: protocol.ResponseBBlockchain, Key: key, Closest: closestPeers(key, respondAddressesMaxDHTB), IsLast: true}
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

		record := protocol.ResponseRecord{Type: protocol.ResponseBFile, Key: key, Closest: closestPeers(key, respondAddressesMaxDHTB), IsLast: true}
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

		record := protocol.ResponseRecord{Type: protocol.ResponseBFile, Key: key, Closest: closestPeers(key, respondAddressesMaxDHTB), IsLast: true}
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
	peer.sendResponse(msg.Sequence, sendUA, records, connection)
}

// peer2Record returns the peer record of the peer to share with a peer that listens on the given IP versions. Nil if the peer has no address to share.
// addressesMax is the maximum count of addresses per family. The addresses are those of the connections to share, see GetConnections2Share.
func (peer *PeerInfo) peer2Record(allowLocal, allowIPv4, allowIPv6 bool, addressesMax int) (result *protocol.PeerRecord) {
	connectionsIPv4 := peer.GetConnections2Share(allowLocal, allowIPv4, false, addressesMax)
	connectionsIPv6 := peer.GetConnections2Share(allowLocal, false, allowIPv6, addressesMax)
	if len(connectionsIPv4) == 0 && len(connectionsIPv6) == 0 {
		return nil
	}

	result = &protocol.PeerRecord{
		PublicKey: peer.PublicKey,
		Features:  peer.Features,
	}

	for _, connection := range connectionsIPv4 {
		result.IPv4 = append(result.IPv4, protocol.PeerRecordAddress{IP: connection.Address.IP, Port: uint16(connection.Address.Port), PortReportedInternal: connection.PortInternal, PortReportedExternal: connection.PortExternal})
	}
	for _, connection := range connectionsIPv6 {
		result.IPv6 = append(result.IPv6, protocol.PeerRecordAddress{IP: connection.Address.IP, Port: uint16(connection.Address.Port), PortReportedInternal: connection.PortInternal, PortReportedExternal: connection.PortExternal})
	}

	return result
}

// limitPeerRecordsSize removes addresses from the peer records until their total size is within sizeMax. Each peer record keeps one address per family.
// The third addresses are removed before the second ones, and those of the last record first: the records are sorted by distance to the key, the addresses by preference.
func limitPeerRecordsSize(records []protocol.PeerRecord, sizeMax int) {
	size := 0
	for n := range records {
		size += records[n].Size()
	}

	for keep := protocol.PeerRecordAddressesMax - 1; keep >= 1 && size > sizeMax; keep-- {
		for n := len(records) - 1; n >= 0 && size > sizeMax; n-- {
			if len(records[n].IPv6) > keep {
				size -= records[n].Size()
				records[n].IPv6 = records[n].IPv6[:keep]
				size += records[n].Size()
			}
			if size > sizeMax && len(records[n].IPv4) > keep {
				size -= records[n].Size()
				records[n].IPv4 = records[n].IPv4[:keep]
				size += records[n].Size()
			}
		}
	}
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

			peer.cmdResponseBootstrapFindSelf(msg, record.Closest, connection.IsLocal())
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

				info.QueueResult(&dht.NodeMessage{SenderID: peer.PublicKey, Closest: peer.records2Nodes(record.Closest, connection.IsLocal()), Storing: peer.records2Nodes(storing, connection.IsLocal())})

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
