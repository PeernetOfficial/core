/*
File Name:  Kademlia_test.go
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
	"time"

	"github.com/PeernetOfficial/core/dht"
)

// TestFindNode checks the peer that FindNode returns. A peer of the peer list reports the peer searched for.
// The peer searched for answers directly and is returned from the peer list, it answers without entering the peer list and is returned as reported, or it does not answer and is not found.
func TestFindNode(t *testing.T) {
	for _, test := range []string{"answers directly", "answers via a relay", "does not answer"} {
		backend, network := testBackendLoopback(t)
		backend.nodesDHT.TimeoutSearch = time.Second
		backend.nodesDHT.TimeoutIR = 100 * time.Millisecond

		reporter, _, _ := testPeerSocket(t, backend, network)

		publicKey, _, _ := ed25519.GenerateKey(rand.Reader)
		address := &net.UDPAddr{IP: net.ParseIP("203.0.113.1"), Port: 112}
		reported := &PeerInfo{Backend: backend, PublicKey: publicKey, isVirtual: true, targetAddresses: []*peerAddress{{IP: address.IP, Port: 112, PortInternal: 112}}}

		backend.nodesDHT.SendRequest = func(request *dht.InformationRequest) {
			for _, node := range request.Nodes {
				switch {
				case bytes.Equal(node.ID, reporter.PublicKey):
					request.QueueResult(&dht.NodeMessage{SenderID: node.ID, Closest: []*dht.Node{{ID: publicKey, Info: reported}}})
					request.Done()

				case test == "does not answer":

				case bytes.Equal(node.ID, publicKey):
					// A direct answer adds the peer to the peer list before the response is handled.
					if test == "answers directly" {
						backend.PeerlistAdd(publicKey, &Connection{backend: backend, Network: network, Address: address, PortInternal: 112})
					}

					request.QueueResult(&dht.NodeMessage{SenderID: node.ID})
					request.Done()
				}
			}
		}

		_, peer, err := backend.FindNode(publicKey, time.Second)
		if err != nil {
			t.Fatalf("peer %s: %v", test, err)
		}

		switch test {
		case "answers directly":
			if peer == nil || peer.IsVirtual() || peer != backend.PeerlistLookup(publicKey) {
				t.Errorf("peer %s: the peer of the peer list is not returned", test)
			}
		case "answers via a relay":
			if peer != reported {
				t.Errorf("peer %s: the reported peer is not returned", test)
			}
		case "does not answer":
			if peer != nil {
				t.Errorf("peer %s: a peer is returned", test)
			}
		}
	}
}
