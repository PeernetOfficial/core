/*
File Name:  Message Send.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"time"

	"github.com/PeernetOfficial/core/protocol"
	"github.com/btcsuite/btcd/btcec"
)

// pingConnection sends a ping to the target peer via the specified connection
func (peer *PeerInfo) pingConnection(connection *Connection) {
	raw := &protocol.PacketRaw{Command: protocol.CommandPing, Sequence: peer.msgNewSequence(nil).sequence}
	Filters.MessageOutPing(peer, raw, connection)

	err := peer.sendConnection(raw, connection)
	connection.LastPingOut = time.Now()

	if (connection.Status == ConnectionActive || connection.Status == ConnectionRedundant) && IsNetworkErrorFatal(err) {
		peer.invalidateActiveConnection(connection)
	}
}

// Chat sends a text message
func (peer *PeerInfo) Chat(text string) {
	peer.send(&protocol.PacketRaw{Command: protocol.CommandChat, Payload: []byte(text)})
}

// sendAnnouncement sends the announcement message. It acquires a new sequence for each message.
func (peer *PeerInfo) sendAnnouncement(sendUA, findSelf bool, findPeer []KeyHash, findValue []KeyHash, files []InfoStore, sequenceData interface{}) (packets []*announcementPacket) {
	packets = msgEncodeAnnouncement(sendUA, findSelf, findPeer, findValue, files)

	for _, packet := range packets {
		packet.sequence = peer.msgNewSequence(sequenceData)
		raw := &protocol.PacketRaw{Command: protocol.CommandAnnouncement, Payload: packet.raw, Sequence: packet.sequence.sequence}
		Filters.MessageOutAnnouncement(peer.PublicKey, peer, raw, findSelf, findPeer, findValue, files)
		packet.err = peer.send(raw)
	}

	return
}

// sendResponse sends the response message
func (peer *PeerInfo) sendResponse(sequence uint32, sendUA bool, hash2Peers []Hash2Peer, filesEmbed []EmbeddedFileData, hashesNotFound [][]byte) (err error) {
	packets, err := msgEncodeResponse(sendUA, hash2Peers, filesEmbed, hashesNotFound)

	for _, packet := range packets {
		raw := &protocol.PacketRaw{Command: protocol.CommandResponse, Payload: packet, Sequence: sequence}
		Filters.MessageOutResponse(peer, raw, hash2Peers, filesEmbed, hashesNotFound)
		peer.send(raw)
	}

	return err
}

// sendTraverse sends a traverse message
func (peer *PeerInfo) sendTraverse(packet *protocol.PacketRaw, receiverEnd *btcec.PublicKey) (err error) {
	packet.Protocol = ProtocolVersion
	// self-reported ports are not set, as this isn't sent via a specific network but a relay
	//packet.SetSelfReportedPorts(c.Network.SelfReportedPorts())

	embeddedPacketRaw, err := protocol.PacketEncrypt(peerPrivateKey, receiverEnd, packet)
	if err != nil {
		return err
	}

	packetRaw, err := msgEncodeTraverse(peerPrivateKey, embeddedPacketRaw, receiverEnd, peer.PublicKey)
	if err != nil {
		return err
	}

	raw := &protocol.PacketRaw{Command: protocol.CommandTraverse, Payload: packetRaw}

	Filters.MessageOutTraverse(peer, raw, packet, receiverEnd)

	return peer.send(raw)
}
