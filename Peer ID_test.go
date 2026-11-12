/*
File Name:  Peer ID_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"

	"github.com/PeernetOfficial/core/protocol"
)

// TestRecords2Nodes checks which peer records of a response become nodes for the DHT client.
func TestRecords2Nodes(t *testing.T) {
	self, _, _ := ed25519.GenerateKey(rand.Reader)
	sender, _, _ := ed25519.GenerateKey(rand.Reader)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	known, _, _ := ed25519.GenerateKey(rand.Reader)

	backend := &Backend{peerPublicKey: self, peerList: make(map[[ed25519.PublicKeySize]byte]*PeerInfo)}
	peerSource := &PeerInfo{Backend: backend, PublicKey: sender}
	peerKnown := &PeerInfo{Backend: backend, PublicKey: known}
	backend.peerList[publicKey2Array(known)] = peerKnown

	// The sender stores the requested data: it lists its own Peer ID as storing peer record without an address.
	nodes := peerSource.records2Nodes([]protocol.PeerRecord{{PublicKey: sender}}, false)
	if len(nodes) != 1 || nodes[0].Info.(*PeerInfo) != peerSource || !bytes.Equal(nodes[0].ID, sender) {
		t.Fatalf("storing peer record of the sender without an address: %d nodes", len(nodes))
	}

	// A record without an address of any other peer is invalid, also if that peer is in the peer list.
	for _, publicKey := range []ed25519.PublicKey{other, known, self} {
		if nodes := peerSource.records2Nodes([]protocol.PeerRecord{{PublicKey: publicKey}}, false); len(nodes) != 0 {
			t.Fatalf("peer record without an address: %d nodes", len(nodes))
		}
	}

	// A peer of the peer list is used as it is. An unknown peer becomes a virtual peer with the addresses of the record that may be used.
	records := []protocol.PeerRecord{
		{PublicKey: known, IPv4: testRecordAddresses(1001, "203.0.113.7")},
		{PublicKey: other, IPv4: testRecordAddresses(1001, "203.0.113.8", "192.168.1.8", "127.0.0.1"), IPv6: testRecordAddresses(1002, "2001:db8::8", "fe80::8")},
		{PublicKey: self, IPv4: testRecordAddresses(1001, "203.0.113.9")},
	}

	tests := []struct {
		allowLocal bool
		expected   []string
	}{
		{false, []string{"203.0.113.8", "2001:db8::8"}},
		{true, []string{"203.0.113.8", "192.168.1.8", "2001:db8::8"}},
	}

	for _, test := range tests {
		nodes = peerSource.records2Nodes(records, test.allowLocal)
		if len(nodes) != 2 || nodes[0].Info.(*PeerInfo) != peerKnown || !bytes.Equal(nodes[1].ID, other) {
			t.Fatalf("allow local %t: unexpected nodes %v", test.allowLocal, nodes)
		}

		virtual := nodes[1].Info.(*PeerInfo)
		if !virtual.isVirtual || virtual.traversePeer != peerSource || len(virtual.targetAddresses) != len(test.expected) {
			t.Fatalf("allow local %t: unexpected virtual peer with %d addresses", test.allowLocal, len(virtual.targetAddresses))
		}
		for n, address := range virtual.targetAddresses {
			if !address.IP.Equal(net.ParseIP(test.expected[n])) {
				t.Errorf("allow local %t: address %s, expected %s", test.allowLocal, address.IP.String(), test.expected[n])
			}
		}
	}

	// An unknown peer with only addresses that may not be used is skipped.
	if nodes := peerSource.records2Nodes([]protocol.PeerRecord{{PublicKey: other, IPv4: testRecordAddresses(1001, "192.168.1.8", "169.254.1.8")}}, false); len(nodes) != 0 {
		t.Fatalf("peer record with local addresses only: %d nodes", len(nodes))
	}
}
