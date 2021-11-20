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
)

// pingConnection sends a ping to the target peer via the specified connection
func (peer *PeerInfo) pingConnection(connection *Connection) {
	raw := &protocol.PacketRaw{Command: protocol.CommandPing, Sequence: networks.Sequences.NewSequence(peer.PublicKey, &peer.messageSequence, nil).SequenceNumber}
	Filters.MessageOutPing(peer, raw, connection)

	err := peer.sendConnection(raw, connection)
	connection.LastPingOut = time.Now()

	if (connection.Status == ConnectionActive || connection.Status == ConnectionRedundant) && IsNetworkErrorFatal(err) {
		peer.invalidateActiveConnection(connection)
	}
}

// Ping sends a ping. This function exists only for debugging purposes, it should not be used normally.
// This ping is not used for uptime detection and the LastPingOut time in connections is not set.
func (peer *PeerInfo) Ping() {
	peer.send(&protocol.PacketRaw{Command: protocol.CommandPing, Sequence: networks.Sequences.NewSequence(peer.PublicKey, &peer.messageSequence, nil).SequenceNumber})
}

// Chat sends a text message
func (peer *PeerInfo) Chat(text string) {
	peer.send(&protocol.PacketRaw{Command: protocol.CommandChat, Payload: []byte(text)})
}

// sendAnnouncement sends the announcement message. It acquires a new sequence for each message.
func (peer *PeerInfo) sendAnnouncement(sendUA, findSelf bool, findPeer []protocol.KeyHash, findValue []protocol.KeyHash, files []protocol.InfoStore, sequenceData interface{}) {
	_, blockchainHeight, blockchainVersion := UserBlockchain.Header()
	packets := protocol.EncodeAnnouncement(sendUA, findSelf, findPeer, findValue, files, FeatureSupport(), blockchainHeight, blockchainVersion, userAgent)

	for _, packet := range packets {
		raw := &protocol.PacketRaw{Command: protocol.CommandAnnouncement, Payload: packet, Sequence: networks.Sequences.NewSequence(peer.PublicKey, &peer.messageSequence, sequenceData).SequenceNumber}
		Filters.MessageOutAnnouncement(peer.PublicKey, peer, raw, findSelf, findPeer, findValue, files)
		peer.send(raw)
	}
}

// sendResponse sends the response message
func (peer *PeerInfo) sendResponse(sequence uint32, sendUA bool, hash2Peers []protocol.Hash2Peer, filesEmbed []protocol.EmbeddedFileData, hashesNotFound [][]byte) (err error) {
	_, blockchainHeight, blockchainVersion := UserBlockchain.Header()
	packets, err := protocol.EncodeResponse(sendUA, hash2Peers, filesEmbed, hashesNotFound, FeatureSupport(), blockchainHeight, blockchainVersion, userAgent)

	for _, packet := range packets {
		raw := &protocol.PacketRaw{Command: protocol.CommandResponse, Payload: packet, Sequence: sequence}
		Filters.MessageOutResponse(peer, raw, hash2Peers, filesEmbed, hashesNotFound)
		peer.send(raw)
	}

	return err
}

// sendTraverse sends a traverse message
func (peer *PeerInfo) sendTraverse(packet *protocol.PacketRaw, receiverEnd *btcec.PublicKey) (err error) {
	packet.Protocol = protocol.ProtocolVersion
	// self-reported ports are not set, as this isn't sent via a specific network but a relay
	//packet.SetSelfReportedPorts(c.Network.SelfReportedPorts())

	embeddedPacketRaw, err := protocol.PacketEncrypt(peerPrivateKey, receiverEnd, packet)
	if err != nil {
		return err
	}

	packetRaw, err := protocol.EncodeTraverse(peerPrivateKey, embeddedPacketRaw, receiverEnd, peer.PublicKey)
	if err != nil {
		return err
	}

	raw := &protocol.PacketRaw{Command: protocol.CommandTraverse, Payload: packetRaw}

	Filters.MessageOutTraverse(peer, raw, packet, receiverEnd)

	return peer.send(raw)
}

// sendTransfer sends a transfer message
func (peer *PeerInfo) sendTransfer(data []byte, control, transferProtocol uint8, hash []byte, offset, limit uint64, sequenceNumber uint32) (err error) {
	packetRaw, err := protocol.EncodeTransfer(peerPrivateKey, data, control, transferProtocol, hash, offset, limit)
	if err != nil {
		return err
	}

	raw := &protocol.PacketRaw{Command: protocol.CommandTransfer, Payload: packetRaw, Sequence: sequenceNumber}

	//Filters.MessageOutTransfer(peer, raw, control, transferProtocol, hash, offset, limit)

	return peer.send(raw)
}
