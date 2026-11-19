/*
File Name:  Blockchain Record.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner

The blockchain record of DHT B (whitepaper § Announcement): the statement of a peer that it stores a blockchain, signed by that peer.
It is carried in B_STORE_BLOCKCHAIN and, after the peer record of the storing peer, in B_RESPONSE_BLOCKCHAIN.
*/

package protocol

import (
	"crypto/ed25519"
	"encoding/binary"
	"time"
)

// blockchainRecordSignedText is the ASCII text the signed data of a blockchain record starts with.
const blockchainRecordSignedText = "B_STORE_BLOCKCHAIN"

// blockchainRecordSignedSize is the size of the fields of a blockchain record that are signed: Peer ID of the blockchain (32) + version (8) + height (8) + time (4).
const blockchainRecordSignedSize = 52

// A blockchain record is valid if its time is at most BlockchainRecordTimePast in the past and at most BlockchainRecordTimeFuture in the future.
const (
	BlockchainRecordTimePast   = 24 * time.Hour
	BlockchainRecordTimeFuture = time.Hour
)

// blockchainRecordSigned returns the data the signature of a blockchain record is computed over:
// the text B_STORE_BLOCKCHAIN followed by the Peer ID of the blockchain, the version, the height and the time as they are encoded in the record.
func blockchainRecordSigned(blockchainPublicKey ed25519.PublicKey, version, height uint64, recordTime uint32) (signed []byte) {
	signed = make([]byte, 0, len(blockchainRecordSignedText)+blockchainRecordSignedSize)
	signed = append(signed, blockchainRecordSignedText...)
	signed = append(signed, blockchainPublicKey...)
	signed = binary.LittleEndian.AppendUint64(signed, version)
	signed = binary.LittleEndian.AppendUint64(signed, height)
	signed = binary.LittleEndian.AppendUint32(signed, recordTime)

	return signed
}

// BlockchainRecordSign returns the signature of a blockchain record. privateKey is the key of the peer that stores the blockchain.
func BlockchainRecordSign(privateKey ed25519.PrivateKey, blockchainPublicKey ed25519.PublicKey, version, height uint64, recordTime uint32) (signature [ed25519.SignatureSize]byte) {
	copy(signature[:], ed25519.Sign(privateKey, blockchainRecordSigned(blockchainPublicKey, version, height, recordTime)))

	return signature
}

// BlockchainRecordValid checks a blockchain record at the time now. It is valid if its signature verifies against the Peer ID of the peer that stores the blockchain,
// and its time is at most 24 hours in the past and at most 1 hour in the future.
func BlockchainRecordValid(storingPeerPublicKey, blockchainPublicKey ed25519.PublicKey, version, height uint64, recordTime uint32, signature [ed25519.SignatureSize]byte, now time.Time) (valid bool) {
	if len(storingPeerPublicKey) != ed25519.PublicKeySize || len(blockchainPublicKey) != ed25519.PublicKeySize {
		return false
	}

	if age := now.Unix() - int64(recordTime); age > int64(BlockchainRecordTimePast/time.Second) || -age > int64(BlockchainRecordTimeFuture/time.Second) {
		return false
	}

	return ed25519.Verify(storingPeerPublicKey, blockchainRecordSigned(blockchainPublicKey, version, height, recordTime), signature[:])
}
