/*
File Name:  DHT Store.go
Copyright:  2021 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"crypto/ed25519"
)

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
