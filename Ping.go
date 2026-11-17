/*
File Name:  Ping.go
Copyright:  2021 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"time"
)

// pingTime is the time in seconds to send out ping messages
const pingTime = 10

// thresholdBlockchainRefresh is the threshold to refresh the blockchain information by sending an Announcement (and expecting the Response message).
// This helps for keeping the global blockchain cache up to date.
const thresholdBlockchainRefresh = 60 * time.Second

// connectionInvalidate is the threshold in seconds to invalidate formerly active connections that no longer receive incoming packets.
const connectionInvalidate = 22

// connectionRemove is the threshold in seconds to remove inactive connections.
const connectionRemove = 2 * 60

// autoPingAll sends out regular ping messages to all connections of all active peers. This allows to detect invalid connections and eventually drop them.
func (backend *Backend) autoPingAll() {
	for {
		time.Sleep(time.Second)

		backend.pingAll()
	}
}

// pingAll goes once through all connections of all peers. It invalidates active connections without incoming packets, sends the ping messages that are due
// to active peers, and removes expired inactive connections. A peer without a connection is removed from the peer list.
func (backend *Backend) pingAll() {
	thresholdInvalidate1 := time.Now().Add(-connectionInvalidate * time.Second)
	thresholdInvalidate2 := time.Now().Add(-connectionInvalidate * time.Second * 4)
	thresholdPingOut1 := time.Now().Add(-pingTime * time.Second)
	thresholdPingOut2 := time.Now().Add(-pingTime * time.Second * 4)
	thresholdBlockchainRefresh := time.Now().Add(-thresholdBlockchainRefresh)

	for _, peer := range backend.PeerlistGet() {
		// Only active peers are pinged, see IsActive.
		isActive := peer.IsActive()

		// first handle active connections
		for _, connection := range peer.GetConnections(true) {
			thresholdPing := thresholdPingOut1
			thresholdInv := thresholdInvalidate1

			if connection.Status == ConnectionRedundant {
				thresholdPing = thresholdPingOut2
				thresholdInv = thresholdInvalidate2
			}

			if connection.LastPacketIn.Before(thresholdInv) {
				peer.invalidateActiveConnection(connection)
				continue
			}

			if isActive && connection.LastPacketIn.Before(thresholdPing) && connection.LastPingOut.Before(thresholdPing) {
				if connection.Status == ConnectionActive && peer.blockchainLastRefresh.Before(thresholdBlockchainRefresh) {
					peer.pingConnectionAnnouncement(connection)
				} else {
					// just a regular ping otherwise
					peer.pingConnection(connection)
				}
				continue
			}
		}

		// handle inactive connections
		for _, connection := range peer.GetConnections(false) {
			// If the inactive connection is expired, remove it.
			if connection.Expires.Before(time.Now()) {
				peer.removeInactiveConnection(connection)
				continue
			}

			// if no ping was sent recently, send one now
			if isActive && connection.LastPingOut.Before(thresholdPingOut1) {
				peer.pingConnection(connection)
			}
		}

		// A peer without a connection is removed from the peer list, which removes it from the routing table.
		if !peer.hasConnection() {
			backend.PeerlistRemove(peer)
		}
	}
}
