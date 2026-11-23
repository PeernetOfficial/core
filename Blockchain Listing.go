/*
File Name:  Blockchain Listing.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner

Listings of DHT B (whitepaper § Protocol, § Announcement). A listing is the statement of a peer that it stores a blockchain, received in a B_STORE_BLOCKCHAIN request.
The peer that keeps it reports the storing peer when it answers B_FIND_BLOCKCHAIN for that blockchain. Listings are held in memory.
*/

package core

import (
	"crypto/ed25519"
	"math/rand"
	"sync"
	"time"

	"github.com/PeernetOfficial/core/protocol"
)

// listingsExpireInterval is the interval at which listings whose blockchain record is no longer valid are removed.
const listingsExpireInterval = time.Hour

// listingsRecentTime is the age of a blockchain record up to which its listing is reported first. A peer repeats its announce every 12 hours;
// the hour on top is the tolerance for the clock of the peer.
const listingsRecentTime = 13 * time.Hour

// blockchainListing is one listing: a peer that stores a blockchain.
type blockchainListing struct {
	record     protocol.BlockchainStore // Blockchain record, signed by the storing peer
	peerPublic *protocol.PeerRecord     // Peer record of the storing peer to share over a connection that is not local. Nil if it has no address to share over it.
	peerLocal  *protocol.PeerRecord     // Peer record of the storing peer to share over a local connection.
}

// blockchainListings are the listings by the Peer ID of the blockchain and the Peer ID of the storing peer.
type blockchainListings struct {
	sync.RWMutex
	blockchains map[[ed25519.PublicKeySize]byte]map[[ed25519.PublicKeySize]byte]*blockchainListing
}

// listingAdd keeps the blockchain record of a B_STORE_BLOCKCHAIN request from the peer as listing. The record must be valid.
// The addresses of the peer are those to share at this time, one per family. A peer without an address to share is not listed.
// A listing of the peer for the same blockchain is replaced, unless its record is newer.
func (peer *PeerInfo) listingAdd(record *protocol.BlockchainStore) {
	listing := &blockchainListing{
		record:     *record,
		peerPublic: peer.peer2Record(false, true, true, respondAddressesMaxDHTB),
		peerLocal:  peer.peer2Record(true, true, true, respondAddressesMaxDHTB),
	}
	if listing.peerPublic == nil && listing.peerLocal == nil {
		return
	}

	listings := &peer.Backend.listings
	listings.Lock()
	defer listings.Unlock()

	if listings.blockchains == nil {
		listings.blockchains = make(map[[ed25519.PublicKeySize]byte]map[[ed25519.PublicKeySize]byte]*blockchainListing)
	}

	blockchainKey := publicKey2Array(record.PublicKey)
	storing := listings.blockchains[blockchainKey]
	if storing == nil {
		storing = make(map[[ed25519.PublicKeySize]byte]*blockchainListing)
		listings.blockchains[blockchainKey] = storing
	}

	peerKey := publicKey2Array(peer.PublicKey)
	if existing := storing[peerKey]; existing != nil && existing.record.Time > record.Time {
		return
	}

	storing[peerKey] = listing
}

// peerRecord returns the peer record of the storing peer to share with a peer that listens on the given IP versions. Nil if there is no address to share.
// allowLocal specifies whether the peer record is shared over a local connection.
func (listing *blockchainListing) peerRecord(allowLocal, allowIPv4, allowIPv6 bool) (result *protocol.PeerRecord) {
	source := listing.peerPublic
	if allowLocal {
		source = listing.peerLocal
	}
	if source == nil {
		return nil
	}

	result = &protocol.PeerRecord{PublicKey: source.PublicKey, Features: source.Features}
	if allowIPv4 {
		result.IPv4 = source.IPv4
	}
	if allowIPv6 {
		result.IPv6 = source.IPv6
	}
	if len(result.IPv4) == 0 && len(result.IPv6) == 0 {
		return nil
	}

	return result
}

// listingsStoring returns the peers listed as storing the blockchain as storing peer records, at most limit. They are picked at random
// among the listings whose blockchain record is at most 13 hours old at the time now, then at random among the other listings.
// A listing whose blockchain record is no longer valid at the time now is left out, and so is a listing of the requester.
// allowLocal, allowIPv4 and allowIPv6 specify the addresses that may be shared with the requester.
func (backend *Backend) listingsStoring(blockchainPublicKey, requesterPublicKey ed25519.PublicKey, allowLocal, allowIPv4, allowIPv6 bool, limit int, now time.Time) (storing []protocol.StoringPeer) {
	var recent, older []*blockchainListing
	recentThreshold := now.Add(-listingsRecentTime)
	requesterKey := publicKey2Array(requesterPublicKey)

	backend.listings.RLock()
	for peerKey, listing := range backend.listings.blockchains[publicKey2Array(blockchainPublicKey)] {
		if peerKey == requesterKey || !protocol.BlockchainRecordTimeValid(listing.record.Time, now) {
			continue
		}

		if time.Unix(int64(listing.record.Time), 0).Before(recentThreshold) {
			older = append(older, listing)
		} else {
			recent = append(recent, listing)
		}
	}
	backend.listings.RUnlock()

	rand.Shuffle(len(recent), func(i, j int) {
		recent[i], recent[j] = recent[j], recent[i]
	})
	rand.Shuffle(len(older), func(i, j int) {
		older[i], older[j] = older[j], older[i]
	})

	for _, listing := range append(recent, older...) {
		if len(storing) >= limit {
			break
		}

		if peerRecord := listing.peerRecord(allowLocal, allowIPv4, allowIPv6); peerRecord != nil {
			storing = append(storing, protocol.StoringPeer{
				PeerRecord:        *peerRecord,
				BlockchainVersion: listing.record.Version,
				BlockchainHeight:  listing.record.Height,
				Time:              listing.record.Time,
				Signature:         listing.record.Signature,
			})
		}
	}

	return storing
}

// listingsRemoveExpired removes the listings whose blockchain record is no longer valid at the time now.
func (backend *Backend) listingsRemoveExpired(now time.Time) {
	backend.listings.Lock()
	defer backend.listings.Unlock()

	for blockchainKey, storing := range backend.listings.blockchains {
		for peerKey, listing := range storing {
			if !protocol.BlockchainRecordTimeValid(listing.record.Time, now) {
				delete(storing, peerKey)
			}
		}

		if len(storing) == 0 {
			delete(backend.listings.blockchains, blockchainKey)
		}
	}
}

// autoListingsRemoveExpired removes the listings that are no longer valid every hour.
func (backend *Backend) autoListingsRemoveExpired() {
	for {
		time.Sleep(listingsExpireInterval)

		backend.listingsRemoveExpired(time.Now())
	}
}
