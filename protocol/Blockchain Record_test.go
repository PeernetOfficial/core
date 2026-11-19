/*
File Name:  Blockchain Record_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package protocol

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"testing"
	"time"
)

// TestBlockchainRecordSignature checks what the signature of a blockchain record is computed over, and that a record with a field, the signature or the storing peer changed is not valid.
func TestBlockchainRecordSignature(t *testing.T) {
	storingPublicKey, storingPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPublicKey, otherPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	blockchain := testKey(t)

	now := time.Unix(1795000000, 0)
	version, height, recordTime := uint64(2), uint64(12), uint32(now.Unix())

	signature := BlockchainRecordSign(storingPrivateKey, blockchain, version, height, recordTime)

	// The signed data: the ASCII text B_STORE_BLOCKCHAIN (18), Peer ID of the blockchain (32), version (8), height (8), time (4).
	fields := append([]byte{}, blockchain...)
	fields = binary.LittleEndian.AppendUint64(fields, version)
	fields = binary.LittleEndian.AppendUint64(fields, height)
	fields = binary.LittleEndian.AppendUint32(fields, recordTime)
	signed := append([]byte("B_STORE_BLOCKCHAIN"), fields...)

	if len(signed) != 70 || !ed25519.Verify(storingPublicKey, signed, signature[:]) {
		t.Fatal("signature does not verify over the text followed by the fields of the record")
	}

	if !BlockchainRecordValid(storingPublicKey, blockchain, version, height, recordTime, signature, now) {
		t.Fatal("valid blockchain record rejected")
	}

	// a changed field
	if BlockchainRecordValid(storingPublicKey, testKey(t), version, height, recordTime, signature, now) {
		t.Fatal("blockchain record with another Peer ID of the blockchain accepted")
	}
	if BlockchainRecordValid(storingPublicKey, blockchain, version+1, height, recordTime, signature, now) {
		t.Fatal("blockchain record with another version accepted")
	}
	if BlockchainRecordValid(storingPublicKey, blockchain, version, height+1, recordTime, signature, now) {
		t.Fatal("blockchain record with another height accepted")
	}
	if BlockchainRecordValid(storingPublicKey, blockchain, version, height, recordTime+1, signature, now) {
		t.Fatal("blockchain record with another time accepted")
	}
	if BlockchainRecordValid(storingPublicKey, blockchain, height, version, recordTime, signature, now) {
		t.Fatal("blockchain record with version and height exchanged accepted")
	}

	// a changed signature
	for _, n := range []int{0, 31, 32, 63} {
		changed := signature
		changed[n] ^= 1
		if BlockchainRecordValid(storingPublicKey, blockchain, version, height, recordTime, changed, now) {
			t.Fatalf("blockchain record with byte %d of the signature changed accepted", n)
		}
	}

	// another storing peer
	if BlockchainRecordValid(otherPublicKey, blockchain, version, height, recordTime, signature, now) {
		t.Fatal("blockchain record accepted for a peer that did not sign it")
	}
	if BlockchainRecordValid(storingPublicKey, blockchain, version, height, recordTime, BlockchainRecordSign(otherPrivateKey, blockchain, version, height, recordTime), now) {
		t.Fatal("blockchain record signed by another peer accepted")
	}

	// a signature over the fields without the text
	var withoutText [ed25519.SignatureSize]byte
	copy(withoutText[:], ed25519.Sign(storingPrivateKey, fields))
	if BlockchainRecordValid(storingPublicKey, blockchain, version, height, recordTime, withoutText, now) {
		t.Fatal("blockchain record with a signature over the fields without the text accepted")
	}

	// a Peer ID of invalid size
	if BlockchainRecordValid(storingPublicKey[:31], blockchain, version, height, recordTime, signature, now) || BlockchainRecordValid(storingPublicKey, blockchain[:31], version, height, recordTime, signature, now) {
		t.Fatal("blockchain record with a Peer ID of 31 bytes accepted")
	}
}

// TestBlockchainRecordTime checks the time a blockchain record is valid: at most 24 hours in the past and at most 1 hour in the future.
func TestBlockchainRecordTime(t *testing.T) {
	storingPublicKey, storingPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	blockchain := testKey(t)

	now := time.Unix(1795000000, 0)

	tests := []struct {
		offset int64 // time of the record relative to now, in seconds
		valid  bool
	}{
		{0, true},
		{-12 * 3600, true},
		{-24 * 3600, true},
		{-24*3600 - 1, false},
		{-365 * 24 * 3600, false},
		{1800, true},
		{3600, true},
		{3600 + 1, false},
		{365 * 24 * 3600, false},
	}

	for _, test := range tests {
		recordTime := uint32(now.Unix() + test.offset)
		signature := BlockchainRecordSign(storingPrivateKey, blockchain, 1, 5, recordTime)

		if valid := BlockchainRecordValid(storingPublicKey, blockchain, 1, 5, recordTime, signature, now); valid != test.valid {
			t.Errorf("blockchain record with time %d seconds from now: valid %t, expected %t", test.offset, valid, test.valid)
		}
	}
}
