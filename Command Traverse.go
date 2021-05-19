/*
File Name:  Command Traverse.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"errors"
	"net"
	"time"

	"github.com/btcsuite/btcd/btcec"
)

// cmdTraverseForward handles an incoming traverse message that should be forwarded to another peer
func (peer *PeerInfo) cmdTraverseForward(msg *MessageTraverse) {
	// Verify the signature. This makes sure that a fowarded message cannot be replayed by others.
	if !msg.SignerPublicKey.IsEqual(peer.PublicKey) || !msg.SignerPublicKey.IsEqual(msg.SenderPublicKey) {
		return
	}

	// Check expiration
	if msg.Expires.Before(time.Now()) {
		return
	}

	// Check if the target peer is known in the peer list. If not, nothing will be done.
	// The original sender should only send the Traverse message as answer to a Response that contains a reported peer that is behind a NAT.
	// In that case the target peer should be still in this peers' peer list.
	peerTarget := PeerlistLookup(msg.TargetPeer)
	if peerTarget == nil {
		return
	}

	// Get the right IP:Port of the original sender to share to the target peer.
	allowIPv4 := peerTarget.Features&(1<<FeatureIPv4Listen) > 0
	allowIPv6 := peerTarget.Features&(1<<FeatureIPv6Listen) > 0
	connectionIPv4 := peer.GetConnection2Share(false, allowIPv4, false)
	connectionIPv6 := peer.GetConnection2Share(false, false, allowIPv6)

	if connectionIPv4 == nil && connectionIPv6 == nil {
		return
	}

	if err := msgEncodeTraverseSetAddress(msg.Payload, connectionIPv4, connectionIPv6); err != nil {
		return
	}

	peerTarget.send(&PacketRaw{Command: CommandTraverse, Payload: msg.Payload})
}

func (peer *PeerInfo) cmdTraverseReceive(msg *MessageTraverse) {
	if msg.Expires.Before(time.Now()) {
		return
	}

	// already an active connection established? nothing todo.
	peerOriginalSender := PeerlistLookup(msg.SignerPublicKey)
	if peerOriginalSender != nil {
		// could process the packet?
		//if connections := peerOriginalSender.GetConnections(true); len(connections) > 0 {
		//	rawPacketsIncoming <- networkWire{network: connections[0].Network, sender: addressOriginalSender, raw: msg.EmbeddedPacketRaw, receiverPublicKey: peerPublicKey, unicast: true}
		//}
		return
	}

	// ---- fork packetWorker to decode and validate embedded packet ---
	decoded, senderPublicKey, err := PacketDecrypt(msg.EmbeddedPacketRaw, peerPublicKey)
	if err != nil {
		return
	}
	if !senderPublicKey.IsEqual(msg.SignerPublicKey) {
		return
	} else if senderPublicKey.IsEqual(peerPublicKey) {
		return
	} else if decoded.Protocol != 0 {
		return
	} else if decoded.Command != CommandAnnouncement {
		return
	}

	// --------
	virtualMessage := &MessageRaw{PacketRaw: *decoded}
	announce, err := msgDecodeAnnouncement(virtualMessage)
	if err != nil {
		return
	}

	// Proper handling of announcement todo, virtual announcement function
	var hashesNotFound [][]byte
	if announce.Actions&(1<<ActionFindSelf) > 0 {
		hashesNotFound = append(hashesNotFound, peer.NodeID)
	}
	if announce.Actions&(1<<ActionFindPeer) > 0 && len(announce.FindPeerKeys) > 0 {
		for _, findPeer := range announce.FindPeerKeys {
			hashesNotFound = append(hashesNotFound, findPeer.Hash)
		}
	}
	if announce.Actions&(1<<ActionFindValue) > 0 {
		for _, findHash := range announce.FindDataKeys {
			hashesNotFound = append(hashesNotFound, findHash.Hash)
		}
	}

	// TODO
	//peer.sendResponse(announce.Sequence, true, nil, nil, hashesNotFound)
	//packets, err := msgEncodeResponse(true, nil, nil, hashesNotFound)
	//sendAllNetworks()

	if !msg.IPv4.IsUnspecified() {
		addressOriginalSenderIPv4 := &net.UDPAddr{IP: msg.IPv4, Port: int(msg.PortIPv4)}
		if msg.PortIPv4ReportedExternal > 0 {
			addressOriginalSenderIPv4.Port = int(msg.PortIPv4ReportedExternal)
		}

		contactArbitraryPeer(msg.SignerPublicKey, addressOriginalSenderIPv4, 0)
	}

	if !msg.IPv6.IsUnspecified() {
		addressOriginalSenderIPv6 := &net.UDPAddr{IP: msg.IPv6, Port: int(msg.PortIPv6)}
		if msg.PortIPv4ReportedExternal > 0 {
			addressOriginalSenderIPv6.Port = int(msg.PortIPv6ReportedExternal)
		}

		contactArbitraryPeer(msg.SignerPublicKey, addressOriginalSenderIPv6, 0)
	}
}

// createVirtualAnnouncement is temporary code and will be improved.
func createVirtualAnnouncement(network *Network, receiverPublicKey *btcec.PublicKey, sequenceData interface{}) (raw []byte, err error) {
	packets := msgEncodeAnnouncement(true, ShouldSendFindSelf(), nil, nil, nil)
	if len(packets) == 0 || packets[0].err != nil {
		return nil, errors.New("error creating virtual packet")
	}

	packet := &PacketRaw{Command: CommandAnnouncement, Payload: packets[0].raw}
	packet.setSelfReportedPorts(network)

	packet.Sequence = msgArbitrarySequence(receiverPublicKey, sequenceData).sequence
	packet.Protocol = ProtocolVersion

	return PacketEncrypt(peerPrivateKey, receiverPublicKey, packet)
}
