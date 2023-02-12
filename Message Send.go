/*
File Name:  Message Send.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"time"

	"github.com/PeernetOfficial/core/btcec"
	"github.com/PeernetOfficial/core/protocol"
	"github.com/google/uuid"
)

// pingConnection sends a ping to the target peer via the specified connection
func (peer *PeerInfo) pingConnection(connection *Connection) {
	raw := &protocol.PacketRaw{Command: protocol.CommandPing, Sequence: peer.Backend.networks.Sequences.NewSequence(peer.PublicKey, &peer.messageSequence, nil).SequenceNumber}
	peer.Backend.Filters.MessageOutPing(peer, raw, connection)

	err := peer.sendConnection(raw, connection)
	connection.LastPingOut = time.Now()

	if (connection.Status == ConnectionActive || connection.Status == ConnectionRedundant) && IsNetworkErrorFatal(err) {
		peer.invalidateActiveConnection(connection)
	}
}

// pingConnectionAnnouncement sends an empty announcement via a particular connection.
// It has the same effect as ping, but returns the blockchain version and height of the other peer in the Response message, which may be useful for keeping the global blockchain cache up to date.
func (peer *PeerInfo) pingConnectionAnnouncement(connection *Connection) {
	_, blockchainHeight, blockchainVersion := peer.Backend.UserBlockchain.Header()
	packets := protocol.EncodeAnnouncement(false, false, nil, nil, nil, peer.Backend.FeatureSupport(), blockchainHeight, blockchainVersion, peer.Backend.userAgent)
	if len(packets) != 1 {
		return
	}

	raw := &protocol.PacketRaw{Command: protocol.CommandAnnouncement, Payload: packets[0], Sequence: peer.Backend.networks.Sequences.NewSequence(peer.PublicKey, &peer.messageSequence, nil).SequenceNumber}
	peer.Backend.Filters.MessageOutAnnouncement(peer.PublicKey, peer, raw, false, nil, nil, nil)

	err := peer.sendConnection(raw, connection)
	connection.LastPingOut = time.Now()

	if (connection.Status == ConnectionActive || connection.Status == ConnectionRedundant) && IsNetworkErrorFatal(err) {
		peer.invalidateActiveConnection(connection)
	}
}

// Ping sends a ping. This function exists only for debugging purposes, it should not be used normally.
// This ping is not used for uptime detection and the LastPingOut time in connections is not set.
func (peer *PeerInfo) Ping() {
	peer.send(&protocol.PacketRaw{Command: protocol.CommandPing, Sequence: peer.Backend.networks.Sequences.NewSequence(peer.PublicKey, &peer.messageSequence, nil).SequenceNumber})
}

// Chat sends a text message
func (peer *PeerInfo) Chat(text string) {
	peer.send(&protocol.PacketRaw{Command: protocol.CommandChat, Payload: []byte(text)})
}

// sendAnnouncement sends the announcement message. It acquires a new sequence for each message.
func (peer *PeerInfo) sendAnnouncement(sendUA, findSelf bool, findPeer []protocol.KeyHash, findValue []protocol.KeyHash, files []protocol.InfoStore, sequenceData interface{}) {
	_, blockchainHeight, blockchainVersion := peer.Backend.UserBlockchain.Header()
	packets := protocol.EncodeAnnouncement(sendUA, findSelf, findPeer, findValue, files, peer.Backend.FeatureSupport(), blockchainHeight, blockchainVersion, peer.Backend.userAgent)

	for _, packet := range packets {
		raw := &protocol.PacketRaw{Command: protocol.CommandAnnouncement, Payload: packet, Sequence: peer.Backend.networks.Sequences.NewSequence(peer.PublicKey, &peer.messageSequence, sequenceData).SequenceNumber}
		peer.Backend.Filters.MessageOutAnnouncement(peer.PublicKey, peer, raw, findSelf, findPeer, findValue, files)
		peer.send(raw)
	}
}

// sendResponse sends the response message
func (peer *PeerInfo) sendResponse(sequence uint32, sendUA bool, hash2Peers []protocol.Hash2Peer, filesEmbed []protocol.EmbeddedFileData, hashesNotFound [][]byte) (err error) {
	_, blockchainHeight, blockchainVersion := peer.Backend.UserBlockchain.Header()
	packets, err := protocol.EncodeResponse(sendUA, hash2Peers, filesEmbed, hashesNotFound, peer.Backend.FeatureSupport(), blockchainHeight, blockchainVersion, peer.Backend.userAgent)

	for _, packet := range packets {
		raw := &protocol.PacketRaw{Command: protocol.CommandResponse, Payload: packet, Sequence: sequence}
		peer.Backend.Filters.MessageOutResponse(peer, raw, hash2Peers, filesEmbed, hashesNotFound)
		peer.send(raw)
	}

	return err
}

// sendTraverse sends a traverse message
func (peer *PeerInfo) sendTraverse(packet *protocol.PacketRaw, receiverEnd *btcec.PublicKey) (err error) {
	packet.Protocol = protocol.ProtocolVersion
	// self-reported ports are not set, as this isn't sent via a specific network but a relay
	//packet.SetSelfReportedPorts(c.Network.SelfReportedPorts())

	embeddedPacketRaw, err := protocol.PacketEncrypt(peer.Backend.PeerPrivateKey, receiverEnd, packet)
	if err != nil {
		return err
	}

	packetRaw, err := protocol.EncodeTraverse(peer.Backend.PeerPrivateKey, embeddedPacketRaw, receiverEnd, peer.PublicKey)
	if err != nil {
		return err
	}

	raw := &protocol.PacketRaw{Command: protocol.CommandTraverse, Payload: packetRaw}

	peer.Backend.Filters.MessageOutTraverse(peer, raw, packet, receiverEnd)

	return peer.send(raw)
}

// sendTransfer sends a transfer message
func (peer *PeerInfo) sendTransfer(data []byte, control, transferProtocol uint8, hash []byte, offset, limit uint64, sequenceNumber uint32, transferID uuid.UUID, isLite bool) (err error) {
	// Send optionally as lite packet. This bypasses the signing overhead of regular Peernet packets which is CPU intensive and a bottleneck.
	if control == protocol.TransferControlActive && isLite {
		raw, err := protocol.PacketLiteEncode(transferID, data)
		if err != nil {
			return err
		}
		return peer.sendLite(raw)
	}

	packetRaw, err := protocol.EncodeTransfer(peer.Backend.PeerPrivateKey, data, control, transferProtocol, hash, offset, limit, transferID)
	if err != nil {
		return err
	}

	raw := &protocol.PacketRaw{Command: protocol.CommandTransfer, Payload: packetRaw, Sequence: sequenceNumber}

	//Filters.MessageOutTransfer(peer, raw, control, transferProtocol, hash, offset, limit)

	return peer.send(raw)
}

// sendGetBlock sends a get block message
func (peer *PeerInfo) sendGetBlock(data []byte, control uint8, blockchainPublicKey *btcec.PublicKey, limitBlockCount, maxBlockSize uint64, targetBlocks []protocol.BlockRange, sequenceNumber uint32, transferID uuid.UUID, isLite bool) (err error) {
	// Send optionally as lite packet. This bypasses the signing overhead of regular Peernet packets which is CPU intensive and a bottleneck.
	if control == protocol.GetBlockControlActive && isLite {
		raw, err := protocol.PacketLiteEncode(transferID, data)
		if err != nil {
			return err
		}
		return peer.sendLite(raw)
	}

	packetRaw, err := protocol.EncodeGetBlock(peer.Backend.PeerPrivateKey, data, control, blockchainPublicKey, limitBlockCount, maxBlockSize, targetBlocks, transferID)
	if err != nil {
		return err
	}

	raw := &protocol.PacketRaw{Command: protocol.CommandGetBlock, Payload: packetRaw, Sequence: sequenceNumber}

	//Filters.MessageOutGetBlock(peer, raw, control, )

	return peer.send(raw)
}
