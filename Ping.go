/*
File Name:  Ping.go
Copyright:  2021 Peernet s.r.o.
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

// connectionRemove is the threshold in seconds to remove inactive connections in case there is at least one active connection known.
const connectionRemove = 2 * 60

// autoPingAll sends out regular ping messages to all connections of all peers. This allows to detect invalid connections and eventually drop them.
func (backend *Backend) autoPingAll() {
	for {
		time.Sleep(time.Second)
		thresholdInvalidate1 := time.Now().Add(-connectionInvalidate * time.Second)
		thresholdInvalidate2 := time.Now().Add(-connectionInvalidate * time.Second * 4)
		thresholdPingOut1 := time.Now().Add(-pingTime * time.Second)
		thresholdPingOut2 := time.Now().Add(-pingTime * time.Second * 4)
		thresholdBlockchainRefresh := time.Now().Add(-thresholdBlockchainRefresh)

		for _, peer := range backend.PeerlistGet() {
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

				if connection.LastPacketIn.Before(thresholdPing) && connection.LastPingOut.Before(thresholdPing) {
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
				// If the inactive connection is expired, remove it; although only if there is at least one active connection, or two other inactive ones.
				if (len(peer.connectionActive) >= 1 || len(peer.connectionInactive) > 2) && connection.Expires.Before(time.Now()) {
					peer.removeInactiveConnection(connection)
					continue
				}

				// if no ping was sent recently, send one now
				if connection.LastPingOut.Before(thresholdPingOut1) {
					peer.pingConnection(connection)
				}
			}
		}
	}
}
