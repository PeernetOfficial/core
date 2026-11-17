/*
File Name:  Message Send.go
Copyright:  2021 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"time"
	"uuid"

	"github.com/PeernetOfficial/core/protocol"
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
	packets := protocol.EncodeAnnouncement(false, nil, peer.Backend.FeatureSupport(), blockchainHeight, blockchainVersion, peer.Backend.userAgent)
	if len(packets) != 1 {
		return
	}

	raw := &protocol.PacketRaw{Command: protocol.CommandAnnouncement, Payload: packets[0], Sequence: peer.Backend.networks.Sequences.NewSequence(peer.PublicKey, &peer.messageSequence, nil).SequenceNumber}
	peer.Backend.Filters.MessageOutAnnouncement(peer.PublicKey, peer, raw, nil)

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

// sendAnnouncement sends the announcement message with the requests (may be nil). It acquires a new sequence for each message.
func (peer *PeerInfo) sendAnnouncement(sendUA bool, requests *protocol.Requests, sequenceData interface{}) {
	_, blockchainHeight, blockchainVersion := peer.Backend.UserBlockchain.Header()
	packets := protocol.EncodeAnnouncement(sendUA, requests, peer.Backend.FeatureSupport(), blockchainHeight, blockchainVersion, peer.Backend.userAgent)

	for _, packet := range packets {
		raw := &protocol.PacketRaw{Command: protocol.CommandAnnouncement, Payload: packet, Sequence: peer.Backend.networks.Sequences.NewSequence(peer.PublicKey, &peer.messageSequence, sequenceData).SequenceNumber}
		peer.Backend.Filters.MessageOutAnnouncement(peer.PublicKey, peer, raw, requests)
		peer.send(raw)
	}
}

// sendResponse sends the response message with the response records.
// connection is the one the announcement was received on. If it is a local connection, the response is sent via it, see sendConnectionLocal.
func (peer *PeerInfo) sendResponse(sequence uint32, sendUA bool, records []protocol.ResponseRecord, connection *Connection) (err error) {
	_, blockchainHeight, blockchainVersion := peer.Backend.UserBlockchain.Header()
	packets, err := protocol.EncodeResponse(sendUA, records, peer.Backend.FeatureSupport(), blockchainHeight, blockchainVersion, peer.Backend.userAgent)
	if err != nil {
		peer.Backend.LogError("sendResponse", "encoding response to %s: %s\n", hex.EncodeToString(peer.PublicKey), err.Error())
		return err
	}

	for _, packet := range packets {
		raw := &protocol.PacketRaw{Command: protocol.CommandResponse, Payload: packet, Sequence: sequence}
		peer.Backend.Filters.MessageOutResponse(peer, raw, records)

		if connection.IsLocal() {
			peer.sendConnectionLocal(raw, connection)
		} else {
			peer.send(raw)
		}
	}

	return nil
}

// sendTraverse sends a traverse message to the peer, which relays the embedded packet to the end receiver.
func (peer *PeerInfo) sendTraverse(packet *protocol.PacketRaw, receiverEnd ed25519.PublicKey) (err error) {
	packet.Protocol = protocol.ProtocolVersion

	embeddedPacketRaw, err := protocol.PacketEncryptEmbedded(peer.Backend.peerKeyPair, receiverEnd, peer.PublicKey, packet)
	if err != nil {
		return err
	}

	packetRaw, err := protocol.EncodeTraverse(embeddedPacketRaw, receiverEnd)
	if err != nil {
		return err
	}

	raw := &protocol.PacketRaw{Command: protocol.CommandTraverse, Payload: packetRaw}

	peer.Backend.Filters.MessageOutTraverse(peer, raw, packet, receiverEnd)

	return peer.send(raw)
}

// sendRelay sends a packet to the end receiver in a relay message via the peer, which is the relay. The packet must be an Announcement or a Response.
// The embedded packet is encrypted for the end receiver, with the Peer ID of the relay followed by the command number of the Relay message as additional authenticated data.
func (peer *PeerInfo) sendRelay(packet *protocol.PacketRaw, receiverEnd ed25519.PublicKey) (err error) {
	switch packet.Command {
	case protocol.CommandAnnouncement:
		if packet, err = relayAnnouncement(packet); err != nil {
			return err
		}
	case protocol.CommandResponse:
	default:
		return errors.New("relay: packet is not an Announcement or a Response")
	}

	packet.Protocol = protocol.ProtocolVersion

	embeddedPacketRaw, err := protocol.PacketEncryptRelay(peer.Backend.peerKeyPair, receiverEnd, peer.PublicKey, packet)
	if err != nil {
		return err
	}

	packetRaw, err := protocol.EncodeRelay(embeddedPacketRaw, receiverEnd, false)
	if err != nil {
		return err
	}

	return peer.send(&protocol.PacketRaw{Command: protocol.CommandRelay, Payload: packetRaw})
}

// relayTimeout is the time to wait for a Response to an Announcement that was sent directly and in a Traverse message, before it is sent in a Relay message.
const relayTimeout = 2 * time.Second

// sendRelayNoResponse sends the Announcement to the end receiver in a relay message via the peer, if the end receiver does not respond within relayTimeout.
// packet is the Announcement that was sent to the end receiver directly and in a Traverse message. The peer is the one that reported the end receiver.
// The end receiver responded if it is in the peer list, which the sender of every received packet is added to.
// The Announcement in the relay message has the same sequence: a Response that arrives directly and via the relay is processed once.
func (peer *PeerInfo) sendRelayNoResponse(packet *protocol.PacketRaw, receiverEnd ed25519.PublicKey) {
	time.AfterFunc(relayTimeout, func() {
		if peer.Backend.PeerlistLookup(receiverEnd) == nil {
			peer.sendRelay(packet, receiverEnd)
		}
	})
}

// relayAnnouncement returns the Announcement as it is sent in a relay message. An Announcement in a relay message carries no B_FIND_FILE_EMBED request:
// such requests are sent as B_FIND_FILE. An Announcement without them is returned as is.
func relayAnnouncement(packet *protocol.PacketRaw) (relayed *protocol.PacketRaw, err error) {
	announce, err := protocol.DecodeAnnouncement(&protocol.MessageRaw{PacketRaw: *packet})
	if err != nil {
		return nil, err
	} else if len(announce.FindFileEmbed) == 0 {
		return packet, nil
	}

	requests := announce.Requests
	requests.FindFile = append(requests.FindFile, requests.FindFileEmbed...)
	requests.FindFileEmbed = nil

	payloads := protocol.EncodeAnnouncement(announce.UserAgent != "", &requests, announce.Features, announce.BlockchainHeight, announce.BlockchainVersion, announce.UserAgent)
	if len(payloads) != 1 {
		return nil, errors.New("relay: announcement does not fit into one message")
	}

	relayed = &protocol.PacketRaw{Protocol: packet.Protocol, Command: packet.Command, Sequence: packet.Sequence, Payload: payloads[0]}
	relayed.SetSelfReportedPorts(announce.PortInternal, announce.PortExternal)

	return relayed, nil
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

	packetRaw, err := protocol.EncodeTransfer(peer.Backend.peerPrivateKey, data, control, transferProtocol, hash, offset, limit, transferID)
	if err != nil {
		return err
	}

	raw := &protocol.PacketRaw{Command: protocol.CommandTransfer, Payload: packetRaw, Sequence: sequenceNumber}

	//Filters.MessageOutTransfer(peer, raw, control, transferProtocol, hash, offset, limit)

	return peer.send(raw)
}

// sendGetBlock sends a get block message
func (peer *PeerInfo) sendGetBlock(data []byte, control uint8, blockchainPublicKey ed25519.PublicKey, limitBlockCount, maxBlockSize uint64, targetBlocks []protocol.BlockRange, sequenceNumber uint32, transferID uuid.UUID, isLite bool) (err error) {
	// Send optionally as lite packet. This bypasses the signing overhead of regular Peernet packets which is CPU intensive and a bottleneck.
	if control == protocol.GetBlockControlActive && isLite {
		raw, err := protocol.PacketLiteEncode(transferID, data)
		if err != nil {
			return err
		}
		return peer.sendLite(raw)
	}

	packetRaw, err := protocol.EncodeGetBlock(peer.Backend.peerPrivateKey, data, control, blockchainPublicKey, limitBlockCount, maxBlockSize, targetBlocks, transferID)
	if err != nil {
		return err
	}

	raw := &protocol.PacketRaw{Command: protocol.CommandGetBlock, Payload: packetRaw, Sequence: sequenceNumber}

	//Filters.MessageOutGetBlock(peer, raw, control, )

	return peer.send(raw)
}
