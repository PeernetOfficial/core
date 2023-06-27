/*
File Username:  Hash.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package protocol

import (
	"github.com/PeernetOfficial/core/btcec"
	"lukechampine.com/blake3"
)

// HashData abstracts the hash function.
func HashData(data []byte) (hash []byte) {
	hash32 := blake3.Sum256(data)
	return hash32[:]
}

// HashSize is blake3 hash digest size = 256 bits
const HashSize = 32

// PublicKey2NodeID translates the Public Key into the node ID used in the Kademlia network.
// It is also referenced in various other places including blockchain data at runtime. The node ID identifies the owner.
func PublicKey2NodeID(publicKey *btcec.PublicKey) (nodeID []byte) {
	return HashData(publicKey.SerializeCompressed())
}
