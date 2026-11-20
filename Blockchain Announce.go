/*
File Name:  Blockchain Announce.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner

Announcing the own blockchain in DHT B (whitepaper § Protocol): a B_STORE_BLOCKCHAIN request to the gamma peers closest to the own Peer ID, repeated every 12 hours.
*/

package core

import (
	"time"

	"github.com/PeernetOfficial/core/protocol"
)

// blockchainAnnounceInterval is the interval at which the own blockchain is announced.
const blockchainAnnounceInterval = 12 * time.Hour

// blockchainAnnounceCheck is the interval at which it is checked whether an announce of the own blockchain is due.
const blockchainAnnounceCheck = time.Minute

// announceBlockchain announces the own blockchain: it sends a B_STORE_BLOCKCHAIN request with one blockchain record, signed at the current time,
// to the gamma closest peers to the own Peer ID in the routing table. It returns the count of peers the request was sent to.
// A blockchain of height 0 is not announced.
func (backend *Backend) announceBlockchain() (sent int) {
	_, height, version := backend.UserBlockchain.Header()
	if height == 0 {
		return 0
	}

	record := protocol.BlockchainStore{PublicKey: backend.peerPublicKey, Version: version, Height: height, Time: uint32(time.Now().Unix())}
	record.Signature = protocol.BlockchainRecordSign(backend.peerPrivateKey, record.PublicKey, record.Version, record.Height, record.Time)

	for _, node := range backend.nodesDHT.GetClosestContacts(gamma, backend.peerPublicKey, nil) {
		node.Info.(*PeerInfo).sendAnnouncement(false, &protocol.Requests{StoreBlockchain: []protocol.BlockchainStore{record}}, nil)
		sent++
	}

	return sent
}

// announceBlockchainDue announces the own blockchain if an announce is due at the time now: it was not announced yet, or the last announce is 12 hours ago.
// last is the time of the last announce. It returns the time of the last announce after the call.
// An announce that was sent to no peer does not count: the next call announces again.
func (backend *Backend) announceBlockchainDue(last, now time.Time) time.Time {
	if !last.IsZero() && now.Sub(last) < blockchainAnnounceInterval {
		return last
	}

	if backend.announceBlockchain() == 0 {
		return last
	}

	return now
}

// autoAnnounceBlockchain announces the own blockchain every 12 hours. Whether an announce is due is checked every minute:
// the first announce is sent once the blockchain has a block and the routing table holds a peer.
func (backend *Backend) autoAnnounceBlockchain() {
	var last time.Time

	for {
		last = backend.announceBlockchainDue(last, time.Now())

		time.Sleep(blockchainAnnounceCheck)
	}
}
