/*
File Name:  Command Relay.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner

A Relay message carries an Announcement or a Response between 2 peers that cannot establish a direct connection.
The message has the same form when sent to the relay and when sent by the relay. A peer that receives one is the end receiver
if the embedded packet is a valid packet from the peer named in the message to itself. Otherwise it is the relay.

For now the 2 peers keep no state about each other: a peer that is reached via a relay is not added to the peer list and the routing table, and it is not pinged.
Every Announcement to a reported peer is first sent directly and in a Traverse message, and in a Relay message if no Response arrives, see sendRelayNoResponse.
*/

package core

import (
	"crypto/ed25519"
	"math/rand"

	"github.com/PeernetOfficial/core/protocol"
)

// cmdRelay handles an incoming relay message. peer is the sender of the message.
func (peer *PeerInfo) cmdRelay(msg *protocol.MessageRelay) {
	if msg.Peer.Equal(peer.Backend.peerPublicKey) || msg.Peer.Equal(peer.PublicKey) {
		return
	}

	// This peer is the end receiver if the embedded packet is a valid packet from the peer named in the message to it. The sender of the message is then the relay.
	// The additional authenticated data of the embedded packet is the Peer ID of the relay followed by the command number of the Relay message.
	if decoded, err := protocol.PacketDecryptRelay(msg.EmbeddedPacketRaw, peer.Backend.peerKeyPair, msg.Peer, peer.PublicKey, peer.Backend.packetKeys(msg.Peer)); err == nil {
		peer.cmdRelayReceive(msg.Peer, decoded)
		return
	}

	peer.cmdRelayForward(msg)
}

// cmdRelayForward forwards a relay message to the peer named in it, if that peer is in the peer list. peer is the original sender.
// The Peer ID in the message is replaced with the one of the original sender. The embedded packet is forwarded unchanged; the relay (self) cannot read it.
func (peer *PeerInfo) cmdRelayForward(msg *protocol.MessageRelay) {
	peerTarget := peer.Backend.PeerlistLookup(msg.Peer)
	if peerTarget == nil {
		return
	}

	payload, err := protocol.EncodeRelay(msg.EmbeddedPacketRaw, peer.PublicKey)
	if err != nil {
		return
	}

	peerTarget.send(&protocol.PacketRaw{Command: protocol.CommandRelay, Payload: payload})
}

// cmdRelayReceive handles the packet embedded in a relay message for this peer. peer is the relay that sent the message, senderPublicKey the original sender.
// The embedded packet must be an Announcement or a Response. It is processed as if it were sent from the original sender directly.
// The original sender is a virtual peer that is reached via the relay: a Response to it is sent in a relay message to the relay, see sendRelay.
func (peer *PeerInfo) cmdRelayReceive(senderPublicKey ed25519.PublicKey, decoded *protocol.PacketRaw) {
	if decoded.Protocol != 0 {
		return
	}

	raw := &protocol.MessageRaw{SenderPublicKey: senderPublicKey, PacketRaw: *decoded}
	peerV := &PeerInfo{Backend: peer.Backend, PublicKey: senderPublicKey, messageSequence: rand.Uint32(), isVirtual: true, relayPeer: peer}

	switch decoded.Command {
	case protocol.CommandAnnouncement:
		if announce, _ := protocol.DecodeAnnouncement(raw); announce != nil {
			if len(announce.UserAgent) > 0 {
				peerV.UserAgent = announce.UserAgent
			}
			peerV.Features = announce.Features

			peer.Backend.Filters.MessageIn(peerV, raw, announce)

			// A file is not embedded in a Response that is sent in a relay message: a B_FIND_FILE_EMBED request is answered as B_FIND_FILE.
			announce.FindFile = append(announce.FindFile, announce.FindFileEmbed...)
			announce.FindFileEmbed = nil

			peerV.cmdAnouncement(announce, nil)
		}

	case protocol.CommandResponse:
		if response, _ := protocol.DecodeResponse(raw); response != nil {
			// Validate sequence number which prevents unsolicited responses.
			isLast := response.IsLast()
			sequenceInfo, valid, _ := peer.Backend.networks.Sequences.ValidateSequence(raw.SenderPublicKey, raw.Sequence, isLast, !isLast)
			if !valid {
				return
			}
			raw.SequenceInfo = sequenceInfo

			if len(response.UserAgent) > 0 {
				peerV.UserAgent = response.UserAgent
			}
			peerV.Features = response.Features

			peer.Backend.Filters.MessageIn(peerV, raw, response)

			peerV.cmdResponse(response, nil)
		}
	}
}
