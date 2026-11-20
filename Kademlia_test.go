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
	"github.com/PeernetOfficial/core/protocol"
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

// TestFindBlockchainRequest checks that a lookup in DHT B sends an Announcement with a B_FIND_BLOCKCHAIN request for the key to the peers of the routing table.
func TestFindBlockchainRequest(t *testing.T) {
	backend, network := testBackendLoopback(t)
	backend.initKademlia()

	peer, _, _ := testPeerSocket(t, backend, network)

	type sent struct {
		receiver ed25519.PublicKey
		requests *protocol.Requests
	}
	out := make(chan sent, 10)
	backend.Filters.MessageOutAnnouncement = func(receiverPublicKey ed25519.PublicKey, peer *PeerInfo, packet *protocol.PacketRaw, requests *protocol.Requests) {
		out <- sent{receiverPublicKey, requests}
	}

	key, _, _ := ed25519.GenerateKey(rand.Reader)

	backend.nodesDHT.TimeoutSearch = time.Second
	backend.nodesDHT.TimeoutIR = 200 * time.Millisecond

	search, err := backend.FindBlockchain(key)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case message := <-out:
		if !message.receiver.Equal(peer.PublicKey) || message.requests == nil || len(message.requests.FindBlockchain) != 1 || !bytes.Equal(message.requests.FindBlockchain[0], key) || message.requests.FindSelf || len(message.requests.FindPeer) != 0 || len(message.requests.StoreBlockchain) != 0 {
			t.Fatalf("unexpected Announcement: %+v", message.requests)
		}
	case <-time.After(time.Second):
		t.Fatal("no Announcement sent")
	}

	// The peer does not answer: no peer is left to ask, the lookup ends.
	select {
	case result := <-search.Storing:
		t.Fatalf("unexpected storing peer %x", result.StoringNode.ID)
	case <-search.TerminateSignal:
	case <-time.After(time.Second):
		t.Fatal("the lookup did not end")
	}
}

// TestResponseBlockchain checks what a lookup takes from a Response: the response records of the type that answers the request,
// and of B_RESPONSE_BLOCKCHAIN the storing peers whose blockchain record is valid.
func TestResponseBlockchain(t *testing.T) {
	backend, network := testBackendLoopback(t)

	sender, senderPrivateKey, _ := testPeerSocket(t, backend, network)
	known, knownPrivateKey, _ := testPeerSocket(t, backend, network)

	blockchainKey, _, _ := ed25519.GenerateKey(rand.Reader)
	otherKey, _, _ := ed25519.GenerateKey(rand.Reader)
	now := uint32(time.Now().Unix())

	// storing returns a storing peer record for the blockchain with a time relative to now, signed by the key. The peer has the addresses given.
	storing := func(publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey, signedBlockchain ed25519.PublicKey, height uint64, timeOffset int64, ips ...string) (record protocol.StoringPeer) {
		record = protocol.StoringPeer{PeerRecord: protocol.PeerRecord{PublicKey: publicKey, IPv4: testRecordAddresses(112, ips...)}, BlockchainVersion: 1, BlockchainHeight: height, Time: uint32(int64(now) + timeOffset)}
		record.Signature = protocol.BlockchainRecordSign(privateKey, signedBlockchain, record.BlockchainVersion, record.BlockchainHeight, record.Time)
		return record
	}

	newKey := func() (ed25519.PublicKey, ed25519.PrivateKey) {
		publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
		return publicKey, privateKey
	}

	reportedKey, reportedPrivateKey := newKey()
	wrongSignatureKey, _ := newKey()
	_, wrongPrivateKey := newKey()
	expiredKey, expiredPrivateKey := newKey()
	otherBlockchainKey, otherBlockchainPrivateKey := newKey()
	noAddressKey, noAddressPrivateKey := newKey()

	changedHeight := storing(reportedKey, reportedPrivateKey, blockchainKey, 20, 0, "203.0.113.20")
	changedHeight.BlockchainHeight++

	closest := []protocol.PeerRecord{{PublicKey: otherKey, IPv4: testRecordAddresses(112, "203.0.113.9")}}

	recordBlockchain := protocol.ResponseRecord{Type: protocol.ResponseBBlockchain, Key: blockchainKey, Closest: closest, IsLast: true, Storing: []protocol.StoringPeer{
		storing(sender.PublicKey, senderPrivateKey, blockchainKey, 1, 0),                           // the sender itself, without an address
		storing(reportedKey, reportedPrivateKey, blockchainKey, 2, -3600, "203.0.113.2"),           // a peer that is not in the peer list
		storing(known.PublicKey, knownPrivateKey, blockchainKey, 3, 0, "203.0.113.3"),              // a peer of the peer list
		storing(wrongSignatureKey, wrongPrivateKey, blockchainKey, 4, 0, "203.0.113.4"),            // signed by another key
		storing(expiredKey, expiredPrivateKey, blockchainKey, 5, -25*3600, "203.0.113.5"),          // 25 hours in the past
		storing(otherBlockchainKey, otherBlockchainPrivateKey, otherKey, 6, 0, "203.0.113.6"),      // signed for another blockchain
		storing(noAddressKey, noAddressPrivateKey, blockchainKey, 7, 0),                            // valid, without an address
		storing(backend.peerPublicKey, backend.peerPrivateKey, blockchainKey, 8, 0, "203.0.113.8"), // self
		changedHeight, // height changed after signing
	}}
	recordPeer := protocol.ResponseRecord{Type: protocol.ResponseAPeer, Key: blockchainKey, Closest: closest, IsLast: true}
	recordOtherKey := protocol.ResponseRecord{Type: protocol.ResponseBBlockchain, Key: otherKey, Closest: closest, IsLast: true}

	// response hands the records to cmdResponse as the response to an information request of the action, and returns the results queued.
	response := func(action int) (results []*dht.NodeMessage, done bool) {
		info := backend.nodesDHT.NewInformationRequest(action, blockchainKey, []*dht.Node{{ID: sender.PublicKey, Info: sender}})

		msg := &protocol.MessageResponse{MessageRaw: &protocol.MessageRaw{SequenceInfo: &protocol.SequenceExpiry{Data: info}}, SequenceLast: true}
		msg.Records = []protocol.ResponseRecord{recordPeer, recordOtherKey, recordBlockchain}
		sender.cmdResponse(msg, nil)

		for result := range info.ResultChan {
			results = append(results, result)
		}
		return results, info.IsTerminated
	}

	// A lookup in DHT B takes the B_RESPONSE_BLOCKCHAIN record for the key.
	results, done := response(dht.ActionFindBlockchain)
	if len(results) != 1 || !done {
		t.Fatalf("lookup in DHT B: %d results, done %t, expected 1 result", len(results), done)
	}
	if len(results[0].Closest) != 1 || !bytes.Equal(results[0].Closest[0].ID, otherKey) {
		t.Fatalf("lookup in DHT B: %d closest peers", len(results[0].Closest))
	}

	expected := []struct {
		publicKey ed25519.PublicKey
		height    uint64
		timeAgo   int64
		peer      *PeerInfo // nil for a peer that is not in the peer list
	}{
		{sender.PublicKey, 1, 0, sender},
		{reportedKey, 2, 3600, nil},
		{known.PublicKey, 3, 0, known},
	}

	if len(results[0].Storing) != len(expected) {
		t.Fatalf("lookup in DHT B: %d storing peers, expected %d", len(results[0].Storing), len(expected))
	}
	for n, node := range results[0].Storing {
		info, ok := node.Info.(*BlockchainStoring)
		if !ok || !bytes.Equal(node.ID, expected[n].publicKey) || !info.Peer.PublicKey.Equal(expected[n].publicKey) {
			t.Fatalf("lookup in DHT B: storing peer %d is %x", n, node.ID)
		}
		if info.Version != 1 || info.Height != expected[n].height || info.Time.Unix() != int64(now)-expected[n].timeAgo {
			t.Errorf("lookup in DHT B: storing peer %d with version %d, height %d, time %s", n, info.Version, info.Height, info.Time)
		}
		if expected[n].peer != nil && info.Peer != expected[n].peer {
			t.Errorf("lookup in DHT B: storing peer %d is not the peer of the peer list", n)
		}
		if expected[n].peer == nil && (!info.Peer.IsVirtual() || len(info.Peer.targetAddresses) != 1 || !info.Peer.targetAddresses[0].IP.Equal(net.ParseIP("203.0.113.2"))) {
			t.Errorf("lookup in DHT B: storing peer %d is not a virtual peer with the address reported", n)
		}
	}

	// A lookup for a peer takes the A_RESPONSE_PEER record for the key.
	results, done = response(dht.ActionFindNode)
	if len(results) != 1 || !done || len(results[0].Closest) != 1 || len(results[0].Storing) != 0 {
		t.Fatalf("lookup for a peer: %d results, done %t, expected 1 result without storing peers", len(results), done)
	}
}

// TestBootstrapKademlia checks that the routing table refresh at startup runs once the routing table holds a peer:
// a peer that is added while bootstrapKademlia waits for new peers, and a peer that is in the routing table before it starts.
func TestBootstrapKademlia(t *testing.T) {
	for _, test := range []string{"peer added while waiting", "peer added before the start"} {
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

		var peer *PeerInfo
		if test == "peer added before the start" {
			peer = testPeerLoopback(t, backend, network, 1)
		}

		go backend.bootstrapKademlia()

		if test == "peer added while waiting" {
			for n := 0; ; n++ {
				backend.peerlistMutex.Lock()
				registered := len(backend.peerMonitor) > 0
				backend.peerlistMutex.Unlock()

				if registered {
					break
				} else if n == 100 {
					t.Fatalf("%s: bootstrapKademlia does not wait for new peers", test)
				}
				time.Sleep(10 * time.Millisecond)
			}

			peer = testPeerLoopback(t, backend, network, 1)
		}

		select {
		case nodeID := <-requests:
			if !bytes.Equal(nodeID, peer.PublicKey) {
				t.Errorf("%s: the lookup asks a node that is not the peer in the routing table", test)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s: no refresh runs with one peer in the routing table", test)
		}
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
