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

// TestBootstrapKademlia checks that the routing table refresh at startup runs once the routing table holds a peer.
func TestBootstrapKademlia(t *testing.T) {
	backend, network := testBackendLoopback(t)
	backend.nodesDHT.TimeoutSearch = time.Second
	backend.nodesDHT.TimeoutIR = 100 * time.Millisecond

	// Every node asked answers without reporting a peer.
	requests := make(chan []byte, 100)
	backend.nodesDHT.SendRequest = func(request *dht.InformationRequest) {
		for _, node := range request.Nodes {
			select {
			case requests <- node.ID:
			default:
			}

			request.QueueResult(&dht.NodeMessage{SenderID: node.ID})
			request.Done()
		}
	}

	go backend.bootstrapKademlia()

	// The peer is added once bootstrapKademlia waits for new peers.
	for n := 0; ; n++ {
		backend.peerlistMutex.Lock()
		registered := len(backend.peerMonitor) > 0
		backend.peerlistMutex.Unlock()

		if registered {
			break
		} else if n == 100 {
			t.Fatal("bootstrapKademlia does not wait for new peers")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)

	peer := testPeerLoopback(t, backend, network, 1)

	select {
	case nodeID := <-requests:
		if !bytes.Equal(nodeID, peer.PublicKey) {
			t.Error("the lookup asks a node that is not the peer in the routing table")
		}
	case <-time.After(time.Second):
		t.Fatal("no refresh runs with one peer in the routing table")
	}
}

// TestNewNodeAddresses checks which addresses of a peer that is reported again are not yet tried.
func TestNewNodeAddresses(t *testing.T) {
	address := func(ip string, port, portInternal, portExternal uint16) *peerAddress {
		return &peerAddress{IP: net.ParseIP(ip), Port: port, PortInternal: portInternal, PortExternal: portExternal}
	}
	virtual := func(addresses ...*peerAddress) *dht.Node {
		return &dht.Node{Info: &PeerInfo{isVirtual: true, targetAddresses: addresses}}
	}
	connected := &dht.Node{Info: &PeerInfo{}}

	a1 := address("203.0.113.1", 112, 112, 0)
	a2 := address("2001:db8::1", 112, 112, 0)
	a3 := address("203.0.113.3", 112, 112, 0)

	tests := []struct {
		name     string
		known    []*dht.Node
		reported *dht.Node
		expected []*peerAddress // nil = no address is new
	}{
		{"same address", []*dht.Node{virtual(a1, a2)}, virtual(address("203.0.113.1", 112, 112, 0)), nil},
		{"same addresses in two known peers", []*dht.Node{virtual(a1), virtual(a2)}, virtual(a2, a1), nil},
		{"one new address", []*dht.Node{virtual(a1, a2)}, virtual(a1, a3), []*peerAddress{a3}},
		{"new address in the second known peer", []*dht.Node{virtual(a1), virtual(a3)}, virtual(a3, a2), []*peerAddress{a2}},
		{"other port", []*dht.Node{virtual(a1)}, virtual(address("203.0.113.1", 5455, 112, 0)), []*peerAddress{address("203.0.113.1", 5455, 112, 0)}},
		{"other internal port", []*dht.Node{virtual(a1)}, virtual(address("203.0.113.1", 112, 113, 0)), []*peerAddress{address("203.0.113.1", 112, 113, 0)}},
		{"other external port", []*dht.Node{virtual(a1)}, virtual(address("203.0.113.1", 112, 112, 5455)), []*peerAddress{address("203.0.113.1", 112, 112, 5455)}},
		{"reported peer is in the peer list", []*dht.Node{virtual(a1)}, connected, nil},
		{"known peer is in the peer list", []*dht.Node{connected}, virtual(a3), nil},
	}

	for _, test := range tests {
		node := newNodeAddresses(test.known, test.reported)

		if test.expected == nil {
			if node != nil {
				t.Errorf("%s: a peer is returned", test.name)
			}
			continue
		}

		if node != test.reported {
			t.Errorf("%s: the reported peer is not returned", test.name)
			continue
		}

		addresses := node.Info.(*PeerInfo).targetAddresses
		if len(addresses) != len(test.expected) {
			t.Errorf("%s: %d addresses returned, expected %d", test.name, len(addresses), len(test.expected))
			continue
		}
		for n := range addresses {
			if !addresses[n].IP.Equal(test.expected[n].IP) || addresses[n].Port != test.expected[n].Port || addresses[n].PortInternal != test.expected[n].PortInternal || addresses[n].PortExternal != test.expected[n].PortExternal {
				t.Errorf("%s: address %d is %v", test.name, n, *addresses[n])
			}
		}
	}
}
