/*
File Name:  LocalNode_test.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Wesley Coakley
*/

package dht

import (
	"net"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/btcec"
	"github.com/PeerNetOfficial/core"
)

// A string with weird characters we'll use as payload to test
// encryption, decryption, and transmission
const testPayload = "TEST PASS (≧◡≦)"

// Example hex-encoded btcec private key
const testKeystr = "22a47fa09a223f2aa079edf85a7c2d4f8720ee63e502ee2869afab7" +
	"de234b80c"

// Emulate a local node using the above const expressions
func LocalNodeForTesting() *LocalNode {
	privKey, pubKey := FromKeystr(testKeystr)
	id := (*NodeID)(pubKey)

	dut := &LocalNode{
		ID: id,
		Secretkey: privKey,
		Addr: &net.UDPAddr{},
	}

	dut.InitializeKeyring()

	return dut
}

// Decode hex-encoded key string into its individual components
func FromKeystr(key string) (*btcec.PrivateKey, *btcec.PublicKey) {
	pkBytes, err := hex.DecodeString(key)
	if err != nil {
		fmt.Println(err)
		return nil, nil
	}
	privKey, pubKey := btcec.PrivKeyFromBytes(btcec.S256(), pkBytes)
	return privKey, pubKey
}

// Stub a conversation between a local client and "remote" node for testing
// functionality
func DummyConversation() (*Conversation) {
	dut := LocalNodeForTesting()
	dummyKNode := dut.AsKNode()

	return dut.SetUpConversation(dummyKNode)
}

// Test ability to encrypt / decrypt packets in a Conversation between two nodes
func TestMessageEncryption(t *testing.T) {
	convo := DummyConversation()

	// Construct a packet to test encryption
	testPacket := &core.PacketRaw{
		Protocol: 0,
		Command: 0,
		Payload: []byte(testPayload),
	}

	// Encrypt the "egressing" packet
	encData, err := convo.EncryptOutgoing(testPacket)
	if err != nil {
		t.Fatalf("`convo.EncryptOutgoing`: %v", err)
	}

	// Pretend we received this data from a remote node :)

	// Decrypt the "ingressing" packet
	decPacket, err := convo.DecryptIncoming(encData)
	if err != nil {
		t.Fatalf("`convo.DecryptIncoming`: %v", err)
	}

	// Test that the "sent" packet is the same as the "received" packet
	if !(decPacket.Protocol == testPacket.Protocol &&
		decPacket.Command == testPacket.Command &&
		string(decPacket.Payload) == testPayload) {
		t.Fatalf("Decrypted packet did not match input packet")
	}
}
