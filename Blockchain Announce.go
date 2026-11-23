/*
File Name:  Blockchain Announce.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner

Announcing the own blockchain in DHT B (whitepaper § Protocol): a B_STORE_BLOCKCHAIN request to the gamma peers closest to the own Peer ID, repeated every 12 hours,
and sent after a change of the version or height of the blockchain.
*/

package core

import (
	"time"

	"github.com/PeernetOfficial/core/protocol"
)

// blockchainAnnounceInterval is the interval at which the own blockchain is announced.
const blockchainAnnounceInterval = 12 * time.Hour

// blockchainAnnounceChangeInterval is the shortest time between two announces of the own blockchain when its version or height changed.
const blockchainAnnounceChangeInterval = 10 * time.Minute

// blockchainAnnounceCheck is the interval at which it is checked whether an announce of the own blockchain is due.
const blockchainAnnounceCheck = time.Minute

// blockchainAnnounced is an announce of the own blockchain: the time it was sent, and the version and height in its blockchain record.
type blockchainAnnounced struct {
	time    time.Time
	version uint64
	height  uint64
}

// announceBlockchain announces the own blockchain in the given version and height: it sends a B_STORE_BLOCKCHAIN request with one blockchain record,
// signed at the current time, to the gamma closest peers to the own Peer ID in the routing table. It returns the count of peers the request was sent to.
// A blockchain of height 0 is not announced.
func (backend *Backend) announceBlockchain(version, height uint64) (sent int) {
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

// announceBlockchainDue announces the own blockchain if an announce is due at the time now: it was not announced yet, the last announce is 12 hours ago,
// or the version or height of the blockchain differ from the last announce and the last announce is 10 minutes ago.
// last is the last announce. It returns the last announce after the call.
// An announce that was sent to no peer does not count: the next call announces again.
func (backend *Backend) announceBlockchainDue(last blockchainAnnounced, now time.Time) blockchainAnnounced {
	_, height, version := backend.UserBlockchain.Header()

	if !last.time.IsZero() {
		interval := blockchainAnnounceInterval
		if version != last.version || height != last.height {
			interval = blockchainAnnounceChangeInterval
		}

		if now.Sub(last.time) < interval {
			return last
		}
	}

	if backend.announceBlockchain(version, height) == 0 {
		return last
	}

	return blockchainAnnounced{time: now, version: version, height: height}
}

// autoAnnounceBlockchain announces the own blockchain every 12 hours, and after a change of its version or height. Whether an announce is due is checked every minute:
// the first announce is sent once the blockchain has a block and the routing table holds a peer.
func (backend *Backend) autoAnnounceBlockchain() {
	var last blockchainAnnounced

	for {
		last = backend.announceBlockchainDue(last, time.Now())

		time.Sleep(blockchainAnnounceCheck)
	}
}
