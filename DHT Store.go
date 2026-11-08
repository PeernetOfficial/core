/*
File Name:  DHT Store.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"crypto/ed25519"

	"github.com/PeernetOfficial/core/protocol"
	"github.com/PeernetOfficial/core/store"
)

func (backend *Backend) initStore() {
	backend.dhtStore = store.NewMemoryStore()
}

// announcementGetData returns data for an announcement
func (peer *PeerInfo) announcementGetData(hash []byte) (stored bool, data []byte) {
	// TODO: Create RetrieveIfSize to prevent files larger than EmbeddedFileSizeMax from being loaded
	data, found := peer.Backend.dhtStore.Get(hash)
	if !found {
		return false, nil
	}

	if len(data) <= protocol.EmbeddedFileSizeMax {
		return true, data
	}

	return true, nil
}

// blockchainStored reports whether the blockchain of the peer is stored here: the own blockchain or a cached copy. Height and version are those of the stored copy.
func (backend *Backend) blockchainStored(publicKey ed25519.PublicKey) (stored bool, height, version uint64) {
	if publicKey.Equal(backend.peerPublicKey) {
		_, height, version = backend.UserBlockchain.Header()
		return true, height, version
	}

	if backend.GlobalBlockchainCache != nil {
		if header, found, err := backend.GlobalBlockchainCache.Store.ReadBlockchainHeader(publicKey); err == nil && found {
			return true, header.Height, header.Version
		}
	}

	return false, 0, 0
}
