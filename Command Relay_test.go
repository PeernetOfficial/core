/*
File Name:  Command Relay_test.go
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
	"github.com/PeernetOfficial/core/store"
)

// testRelay returns the decoded Relay message as the peer with the public key relaySender sends it. It names the peer.
// The embedded packet is the one of the original sender for the end receiver, embedded for the relay.
func testRelay(t *testing.T, sender *protocol.KeyPair, receiverEnd, relay, peer, relaySender ed25519.PublicKey, command uint8, sequence uint32, payload []byte) (msg *protocol.MessageRelay) {
	embedded, err := protocol.PacketEncryptRelay(sender, receiverEnd, relay, &protocol.PacketRaw{Protocol: protocol.ProtocolVersion, Command: command, Sequence: sequence, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}

	return testRelayEmbedded(t, embedded, peer, relaySender)
}

// testRelayEmbedded returns the decoded Relay message with the embedded packet as the peer with the public key relaySender sends it. It names the peer.
func testRelayEmbedded(t *testing.T, embedded []byte, peer, relaySender ed25519.PublicKey) (msg *protocol.MessageRelay) {
	payload, err := protocol.EncodeRelay(embedded, peer)
	if err != nil {
		t.Fatal(err)
	}
	msg, err = protocol.DecodeRelay(&protocol.MessageRaw{PacketRaw: protocol.PacketRaw{Command: protocol.CommandRelay, Payload: payload}, SenderPublicKey: relaySender})
	if err != nil {
		t.Fatal(err)
	}

	return msg
}

// testRelayOut records the Relay messages and the Responses a backend sends.
type testRelayOut struct {
	relays    []*protocol.MessageRelay // Relay messages sent
	receivers []ed25519.PublicKey      // Peers the Relay messages were sent to
	responses int                      // Count of Responses, sent directly or in a Relay message
}

// testWatchRelay records the Relay messages and the Responses the backend sends.
func testWatchRelay(t *testing.T, backend *Backend) (out *testRelayOut) {
	out = &testRelayOut{}
	backend.Filters.PacketOut = func(packet *protocol.PacketRaw, receiverPublicKey ed25519.PublicKey, connection *Connection) {
		if packet.Command != protocol.CommandRelay {
			return
		}
		msg, err := protocol.DecodeRelay(&protocol.MessageRaw{PacketRaw: *packet})
		if err != nil {
			t.Fatal(err)
		}
		out.relays = append(out.relays, msg)
		out.receivers = append(out.receivers, receiverPublicKey)
	}
	backend.Filters.MessageOutResponse = func(peer *PeerInfo, packet *protocol.PacketRaw, records []protocol.ResponseRecord) {
		out.responses++
	}

	return out
}

func (out *testRelayOut) reset() {
	out.relays, out.receivers, out.responses = nil, nil, 0
}

// testPeerSocket adds a peer with a new key to the peer list of the backend. It is connected via a socket on the loopback address.
func testPeerSocket(t *testing.T, backend *Backend, network *Network) (peer *PeerInfo, privateKey ed25519.PrivateKey, socket *net.UDPConn) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	socket, address := testSocket(t)
	peer, _ = backend.PeerlistAdd(publicKey, &Connection{backend: backend, Network: network, Address: address, PortInternal: uint16(address.Port)})

	return peer, privateKey, socket
}

// TestRelayForward checks that the relay forwards a Relay message to the peer named in it if that peer is in the peer list.
// The forwarded message names the original sender and carries the embedded packet unchanged. The relay cannot read the embedded packet: one that is not an Announcement or a Response is forwarded.
func TestRelayForward(t *testing.T) {
	backend, network := testBackendLoopback(t)
	out := testWatchRelay(t, backend)

	sender, senderPrivateKey, _ := testPeerSocket(t, backend, network)
	target, targetPrivateKey, socketTarget := testPeerSocket(t, backend, network)
	otherPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)

	senderKeyPair := testKeyPair(t, senderPrivateKey)
	announcement := protocol.EncodeAnnouncement(true, &protocol.Requests{FindSelf: true}, 1<<protocol.FeatureIPv4Listen, 0, 0, "test")[0]

	random := make([]byte, protocol.PacketLengthMinRelay)
	rand.Read(random)

	tests := []struct {
		name      string
		msg       *protocol.MessageRelay
		forwarded bool
	}{
		{"embedded Announcement", testRelay(t, senderKeyPair, target.PublicKey, backend.peerPublicKey, target.PublicKey, sender.PublicKey, protocol.CommandAnnouncement, 1, announcement), true},
		{"embedded Ping", testRelay(t, senderKeyPair, target.PublicKey, backend.peerPublicKey, target.PublicKey, sender.PublicKey, protocol.CommandPing, 1, nil), true},
		{"random embedded packet", testRelayEmbedded(t, random, target.PublicKey, sender.PublicKey), true},
		{"named peer is not in the peer list", testRelay(t, senderKeyPair, otherPublicKey, backend.peerPublicKey, otherPublicKey, sender.PublicKey, protocol.CommandAnnouncement, 1, announcement), false},
		{"named peer is the relay", testRelayEmbedded(t, random, backend.peerPublicKey, sender.PublicKey), false},
		{"named peer is the sender", testRelayEmbedded(t, random, sender.PublicKey, sender.PublicKey), false},
	}

	for _, test := range tests {
		out.reset()
		sender.cmdRelay(test.msg)

		if out.responses != 0 {
			t.Errorf("%s: %d Responses sent by the relay, expected none", test.name, out.responses)
		}
		if !test.forwarded {
			if len(out.relays) != 0 {
				t.Errorf("%s: %d Relay messages forwarded, expected none", test.name, len(out.relays))
			}
			continue
		}

		if len(out.relays) != 1 {
			t.Errorf("%s: %d Relay messages forwarded, expected 1", test.name, len(out.relays))
			continue
		} else if !out.receivers[0].Equal(target.PublicKey) || !out.relays[0].Peer.Equal(sender.PublicKey) || !bytes.Equal(out.relays[0].EmbeddedPacketRaw, test.msg.EmbeddedPacketRaw) {
			t.Errorf("%s: forwarded Relay message mismatch", test.name)
		}

		// The forwarded message arrives at the named peer as a packet of the relay.
		packet := testReadPacket(t, test.name, backend, socketTarget, targetPrivateKey, 2*time.Second)
		if packet == nil || packet.Command != protocol.CommandRelay {
			t.Errorf("%s: no Relay message received by the named peer", test.name)
		}
	}
}

// TestRelayReceiveAnnouncement checks that the end receiver answers an Announcement embedded in a Relay message with a Response to the original sender, sent in a Relay message to the relay.
// The original sender decrypts the embedded Response with the Peer ID of the relay. A B_FIND_FILE_EMBED request is answered as B_FIND_FILE.
func TestRelayReceiveAnnouncement(t *testing.T) {
	backend, network := testBackendLoopback(t)
	backend.dhtStore = store.NewMemoryStore()
	out := testWatchRelay(t, backend)

	relay, relayPrivateKey, socketRelay := testPeerSocket(t, backend, network)
	senderPublicKey, senderPrivateKey, _ := ed25519.GenerateKey(rand.Reader)
	senderKeyPair := testKeyPair(t, senderPrivateKey)

	fileData := []byte("stored file")
	fileHash := protocol.HashData(fileData)
	if err := backend.dhtStore.Set(fileHash, fileData); err != nil {
		t.Fatal(err)
	}

	// The Relay message as the relay forwards it.
	announcement := protocol.EncodeAnnouncement(true, &protocol.Requests{FindSelf: true, FindFileEmbed: [][]byte{fileHash}}, 1<<protocol.FeatureIPv4Listen, 0, 0, "test")[0]
	msg := testRelay(t, senderKeyPair, backend.peerPublicKey, relay.PublicKey, senderPublicKey, relay.PublicKey, protocol.CommandAnnouncement, 777, announcement)

	relay.cmdRelay(msg)

	if out.responses != 1 || len(out.relays) != 1 {
		t.Fatalf("%d Responses and %d Relay messages sent, expected 1 and 1", out.responses, len(out.relays))
	} else if !out.receivers[0].Equal(relay.PublicKey) || !out.relays[0].Peer.Equal(senderPublicKey) {
		t.Fatal("the Relay message is not sent to the relay or does not name the original sender")
	}

	// The Relay message arrives at the relay, which forwards the embedded packet to the original sender.
	packet := testReadPacket(t, "relay", backend, socketRelay, relayPrivateKey, 2*time.Second)
	if packet == nil || packet.Command != protocol.CommandRelay {
		t.Fatal("no Relay message received by the relay")
	}
	answer, err := protocol.DecodeRelay(&protocol.MessageRaw{PacketRaw: *packet})
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := protocol.PacketDecryptRelay(answer.EmbeddedPacketRaw, senderKeyPair, backend.peerPublicKey, relay.PublicKey, nil)
	if err != nil {
		t.Fatalf("embedded packet of the answer: %v", err)
	} else if decoded.Command != protocol.CommandResponse || decoded.Sequence != 777 {
		t.Fatalf("embedded packet of the answer has command %d sequence %d, expected a Response with sequence 777", decoded.Command, decoded.Sequence)
	}

	response, err := protocol.DecodeResponse(&protocol.MessageRaw{PacketRaw: *decoded})
	if err != nil {
		t.Fatal(err)
	} else if len(response.Records) != 2 || response.Records[0].Type != protocol.ResponseASelf || response.Records[1].Type != protocol.ResponseBFile {
		t.Fatalf("response records %+v, expected A_RESPONSE_SELF and B_RESPONSE_FILE", response.Records)
	} else if len(response.Records[1].Storing) != 1 || !response.Records[1].Storing[0].PublicKey.Equal(backend.peerPublicKey) || len(response.Records[1].Data) != 0 {
		t.Fatal("B_RESPONSE_FILE does not list the end receiver as storing peer, or carries the file")
	}

	// The original sender is not added to the peer list.
	if backend.PeerlistLookup(senderPublicKey) != nil {
		t.Error("the original sender is in the peer list")
	}

	// The same Announcement sent directly is answered with the embedded file.
	out.reset()
	var records []protocol.ResponseRecord
	backend.Filters.MessageOutResponse = func(peer *PeerInfo, packet *protocol.PacketRaw, recordsOut []protocol.ResponseRecord) {
		records = recordsOut
	}
	direct, err := protocol.DecodeAnnouncement(&protocol.MessageRaw{PacketRaw: protocol.PacketRaw{Payload: announcement}})
	if err != nil {
		t.Fatal(err)
	}
	(&PeerInfo{Backend: backend, PublicKey: senderPublicKey}).cmdAnouncement(direct, nil)
	if len(records) != 2 || records[1].Type != protocol.ResponseBFileEmbed || !bytes.Equal(records[1].Data, fileData) {
		t.Fatalf("response records to the direct Announcement %+v, expected the embedded file", records)
	}
}

// TestRelayReceiveInvalid checks that the end receiver drops a Relay message whose embedded packet is not an Announcement or a Response.
// A Relay message whose embedded packet is not a valid packet from the named peer to this peer is not answered: this peer is then the relay,
// and forwards the message only if the named peer is in the peer list.
func TestRelayReceiveInvalid(t *testing.T) {
	backend, network := testBackendLoopback(t)
	out := testWatchRelay(t, backend)

	relay, _, _ := testPeerSocket(t, backend, network)
	known, knownPrivateKey, _ := testPeerSocket(t, backend, network)
	otherPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)

	announcement := protocol.EncodeAnnouncement(true, &protocol.Requests{FindSelf: true}, 1<<protocol.FeatureIPv4Listen, 0, 0, "test")[0]
	self := backend.peerPublicKey

	// withoutSender returns a packet without the sender's public key.
	withoutSender := func(raw []byte, err error) []byte {
		if err != nil {
			t.Fatal(err)
		}
		return append(append([]byte{}, raw[:12]...), raw[12+ed25519.PublicKeySize:]...)
	}

	tests := []struct {
		name      string
		responses int
		forwarded int
		msg       func(sender *protocol.KeyPair) *protocol.MessageRelay // the Relay message as the relay delivers it
	}{
		{"embedded Announcement", 1, 0, func(sender *protocol.KeyPair) *protocol.MessageRelay {
			return testRelay(t, sender, self, relay.PublicKey, sender.PublicKey, relay.PublicKey, protocol.CommandAnnouncement, 1, announcement)
		}},
		{"embedded Ping", 0, 0, func(sender *protocol.KeyPair) *protocol.MessageRelay {
			return testRelay(t, sender, self, relay.PublicKey, sender.PublicKey, relay.PublicKey, protocol.CommandPing, 1, nil)
		}},
		{"embedded Chat with the payload of an Announcement", 0, 0, func(sender *protocol.KeyPair) *protocol.MessageRelay {
			return testRelay(t, sender, self, relay.PublicKey, sender.PublicKey, relay.PublicKey, protocol.CommandChat, 1, announcement)
		}},
		{"embedded Relay message", 0, 0, func(sender *protocol.KeyPair) *protocol.MessageRelay {
			inner := testRelay(t, sender, self, relay.PublicKey, sender.PublicKey, relay.PublicKey, protocol.CommandAnnouncement, 1, announcement)
			return testRelay(t, sender, self, relay.PublicKey, sender.PublicKey, relay.PublicKey, protocol.CommandRelay, 1, inner.Payload)
		}},
		{"embedded Response without a request", 0, 0, func(sender *protocol.KeyPair) *protocol.MessageRelay {
			return testRelay(t, sender, self, relay.PublicKey, sender.PublicKey, relay.PublicKey, protocol.CommandResponse, 1, announcement)
		}},
		{"embedded Announcement built for another relay", 0, 0, func(sender *protocol.KeyPair) *protocol.MessageRelay {
			return testRelay(t, sender, self, otherPublicKey, sender.PublicKey, relay.PublicKey, protocol.CommandAnnouncement, 1, announcement)
		}},
		{"embedded Announcement built for another end receiver", 0, 0, func(sender *protocol.KeyPair) *protocol.MessageRelay {
			return testRelay(t, sender, otherPublicKey, relay.PublicKey, sender.PublicKey, relay.PublicKey, protocol.CommandAnnouncement, 1, announcement)
		}},
		{"embedded Announcement of another peer than the named one", 0, 0, func(sender *protocol.KeyPair) *protocol.MessageRelay {
			return testRelay(t, sender, self, relay.PublicKey, otherPublicKey, relay.PublicKey, protocol.CommandAnnouncement, 1, announcement)
		}},
		{"embedded Announcement with a changed byte", 0, 0, func(sender *protocol.KeyPair) *protocol.MessageRelay {
			msg := testRelay(t, sender, self, relay.PublicKey, sender.PublicKey, relay.PublicKey, protocol.CommandAnnouncement, 1, announcement)
			msg.EmbeddedPacketRaw[len(msg.EmbeddedPacketRaw)-1] ^= 1
			return msg
		}},
		{"Announcement embedded for a Traverse message", 0, 0, func(sender *protocol.KeyPair) *protocol.MessageRelay {
			embedded := withoutSender(protocol.PacketEncryptEmbedded(sender, self, relay.PublicKey, &protocol.PacketRaw{Protocol: protocol.ProtocolVersion, Command: protocol.CommandAnnouncement, Payload: announcement}))
			return testRelayEmbedded(t, embedded, sender.PublicKey, relay.PublicKey)
		}},
		{"embedded Announcement of a peer in the peer list for another end receiver", 0, 1, func(sender *protocol.KeyPair) *protocol.MessageRelay {
			return testRelay(t, testKeyPair(t, knownPrivateKey), otherPublicKey, relay.PublicKey, known.PublicKey, relay.PublicKey, protocol.CommandAnnouncement, 1, announcement)
		}},
		{"embedded Announcement of a peer in the peer list", 1, 0, func(sender *protocol.KeyPair) *protocol.MessageRelay {
			return testRelay(t, testKeyPair(t, knownPrivateKey), self, relay.PublicKey, known.PublicKey, relay.PublicKey, protocol.CommandAnnouncement, 1, announcement)
		}},
	}

	for _, test := range tests {
		// Each case has its own original sender.
		_, senderPrivateKey, _ := ed25519.GenerateKey(rand.Reader)

		out.reset()
		relay.cmdRelay(test.msg(testKeyPair(t, senderPrivateKey)))

		// A Response is sent in a Relay message to the relay. A forwarded message is sent to the named peer.
		if out.responses != test.responses || len(out.relays) != test.responses+test.forwarded {
			t.Errorf("%s: %d Responses and %d Relay messages sent, expected %d and %d", test.name, out.responses, len(out.relays), test.responses, test.responses+test.forwarded)
		} else if test.responses == 1 && !out.receivers[0].Equal(relay.PublicKey) || test.forwarded == 1 && (!out.receivers[0].Equal(known.PublicKey) || !out.relays[0].Peer.Equal(relay.PublicKey)) {
			t.Errorf("%s: Relay message sent to an unexpected peer", test.name)
		}
	}
}

// TestRelayReceiveResponse checks that a Response embedded in a Relay message is processed as the response to the Announcement with its sequence, once.
// The peers it reports are not sent Traverse messages via the peer that is reached via the relay.
func TestRelayReceiveResponse(t *testing.T) {
	backend, network := testBackendLoopback(t)

	relay, _, _ := testPeerSocket(t, backend, network)
	senderPublicKey, senderPrivateKey, _ := ed25519.GenerateKey(rand.Reader)
	senderKeyPair := testKeyPair(t, senderPrivateKey)

	key, _, _ := ed25519.GenerateKey(rand.Reader)
	reportedPublicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	reported := protocol.PeerRecord{PublicKey: reportedPublicKey, Features: 1 << protocol.FeatureIPv4Listen, IPv4: []protocol.PeerRecordAddress{{IP: net.ParseIP("203.0.113.7").To4(), Port: 1001, PortReportedInternal: 1001}}}

	payloads, err := protocol.EncodeResponse(true, []protocol.ResponseRecord{{Type: protocol.ResponseAPeer, Key: key, Closest: []protocol.PeerRecord{reported}, IsLast: true}}, 1<<protocol.FeatureIPv4Listen, 0, 0, "test")
	if err != nil || len(payloads) != 1 {
		t.Fatalf("encoding response: %d messages, %v", len(payloads), err)
	}

	// The information request of the Announcement that was sent to the peer.
	info := backend.nodesDHT.NewInformationRequest(dht.ActionFindNode, key, []*dht.Node{{ID: senderPublicKey}})
	var messageSequence uint32
	sequence := backend.networks.Sequences.NewSequence(senderPublicKey, &messageSequence, info).SequenceNumber

	result := func() *dht.NodeMessage {
		select {
		case message := <-info.ResultChan:
			return message
		default:
			return nil
		}
	}

	// A Response with another sequence is dropped.
	relay.cmdRelay(testRelay(t, senderKeyPair, backend.peerPublicKey, relay.PublicKey, senderPublicKey, relay.PublicKey, protocol.CommandResponse, sequence+1, payloads[0]))
	if result() != nil {
		t.Fatal("Response with an unknown sequence processed")
	}

	msg := testRelay(t, senderKeyPair, backend.peerPublicKey, relay.PublicKey, senderPublicKey, relay.PublicKey, protocol.CommandResponse, sequence, payloads[0])
	relay.cmdRelay(msg)

	message := result()
	if message == nil {
		t.Fatal("Response not processed")
	} else if !bytes.Equal(message.SenderID, senderPublicKey) || len(message.Closest) != 1 || !bytes.Equal(message.Closest[0].ID, reportedPublicKey) {
		t.Fatalf("result %+v, expected the reported peer from the original sender", message)
	}
	if virtual := message.Closest[0].Info.(*PeerInfo); !virtual.isVirtual || virtual.traversePeer != nil || virtual.relayPeer != nil {
		t.Error("the reported peer is not a virtual peer that is contacted directly, without Traverse message")
	}

	// The same Relay message a second time: the sequence is no longer valid.
	relay.cmdRelay(msg)
	if result() != nil {
		t.Fatal("Response processed a second time")
	}

	if backend.PeerlistLookup(senderPublicKey) != nil {
		t.Error("the original sender is in the peer list")
	}
}

// TestSendRelay checks the packets to a virtual peer that is reached via a relay: an Announcement and a Response are sent in a Relay message to the relay, which names the end receiver.
// B_FIND_FILE_EMBED requests of the Announcement are sent as B_FIND_FILE; the other fields are those of the Announcement. Other messages are not sent.
func TestSendRelay(t *testing.T) {
	backend, network := testBackendLoopback(t)
	out := testWatchRelay(t, backend)

	relay, _, _ := testPeerSocket(t, backend, network)
	targetPublicKey, targetPrivateKey, _ := ed25519.GenerateKey(rand.Reader)
	targetKeyPair := testKeyPair(t, targetPrivateKey)
	backend.userAgent = "test/1.0"

	virtual := &PeerInfo{Backend: backend, PublicKey: targetPublicKey, isVirtual: true, relayPeer: relay}

	// embedded returns the packet embedded in the single Relay message that was sent, as the end receiver decrypts it.
	embedded := func(name string) *protocol.PacketRaw {
		if len(out.relays) != 1 {
			t.Fatalf("%s: %d Relay messages sent, expected 1", name, len(out.relays))
		} else if !out.receivers[0].Equal(relay.PublicKey) || !out.relays[0].Peer.Equal(targetPublicKey) {
			t.Fatalf("%s: the Relay message is not sent to the relay or does not name the end receiver", name)
		}
		decoded, err := protocol.PacketDecryptRelay(out.relays[0].EmbeddedPacketRaw, targetKeyPair, backend.peerPublicKey, relay.PublicKey, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out.reset()
		return decoded
	}

	keyPeer, _, _ := ed25519.GenerateKey(rand.Reader)
	keyFile := protocol.HashData([]byte("file 1"))
	keyFileEmbed := protocol.HashData([]byte("file 2"))

	for _, test := range []struct {
		name             string
		requests         *protocol.Requests
		expectedFindFile [][]byte
	}{
		{"Announcement with B_FIND_FILE_EMBED", &protocol.Requests{FindPeer: [][]byte{keyPeer}, FindFile: [][]byte{keyFile}, FindFileEmbed: [][]byte{keyFileEmbed}}, [][]byte{keyFile, keyFileEmbed}},
		{"Announcement without B_FIND_FILE_EMBED", &protocol.Requests{FindSelf: true, FindPeer: [][]byte{keyPeer}, FindFile: [][]byte{keyFile}}, [][]byte{keyFile}},
	} {
		info := &dht.InformationRequest{}
		virtual.sendAnnouncement(true, test.requests, info)

		decoded := embedded(test.name)
		announce, err := protocol.DecodeAnnouncement(&protocol.MessageRaw{PacketRaw: *decoded})
		if err != nil || decoded.Command != protocol.CommandAnnouncement {
			t.Fatalf("%s: embedded packet has command %d: %v", test.name, decoded.Command, err)
		}
		if len(announce.FindFileEmbed) != 0 || len(announce.FindFile) != len(test.expectedFindFile) || announce.FindSelf != test.requests.FindSelf || len(announce.FindPeer) != 1 || !bytes.Equal(announce.FindPeer[0], keyPeer) {
			t.Errorf("%s: requests %+v", test.name, announce.Requests)
		}
		for n := range test.expectedFindFile {
			if n < len(announce.FindFile) && !bytes.Equal(announce.FindFile[n], test.expectedFindFile[n]) {
				t.Errorf("%s: B_FIND_FILE key %d mismatch", test.name, n)
			}
		}
		if announce.UserAgent != "test/1.0" || announce.Features != backend.FeatureSupport() {
			t.Errorf("%s: User Agent %q features %d", test.name, announce.UserAgent, announce.Features)
		}

		// The sequence of the embedded Announcement is the one registered for the request.
		if sequenceInfo, valid, _ := backend.networks.Sequences.ValidateSequence(targetPublicKey, decoded.Sequence, true, false); !valid || sequenceInfo.Data != info {
			t.Errorf("%s: sequence %d of the embedded Announcement is not the one of the request", test.name, decoded.Sequence)
		}
	}

	if err := virtual.sendResponse(12345, true, nil, nil); err != nil {
		t.Fatal(err)
	}
	if decoded := embedded("Response"); decoded.Command != protocol.CommandResponse || decoded.Sequence != 12345 {
		t.Errorf("Response: embedded packet has command %d sequence %d", decoded.Command, decoded.Sequence)
	}

	// Other messages are not sent via a relay.
	virtual.Ping()
	virtual.Chat("hello")
	if err := virtual.sendTraverse(&protocol.PacketRaw{Command: protocol.CommandAnnouncement}, targetPublicKey); err == nil {
		t.Error("Traverse message sent via a relay")
	}
	if len(out.relays) != 0 {
		t.Errorf("%d Relay messages sent for a Ping, a Chat and a Traverse message, expected none", len(out.relays))
	}
}

// testDeliver reads the next packet that arrives at the socket of the backend and lets the packet worker of the backend process it. It returns false if none arrives.
func testDeliver(backend *Backend, network *Network) (delivered bool) {
	buffer := make([]byte, maxPacketSize)
	network.socket.SetReadDeadline(time.Now().Add(2 * time.Second))
	length, sender, err := network.socket.ReadFromUDP(buffer)
	if err != nil {
		return false
	}

	testPacketWorker(backend, networkWire{network: network, sender: sender, raw: buffer[:length], receiverKeyPair: backend.peerKeyPair, unicast: true})
	return true
}

// TestRelayRoundTrip sends an Announcement from a peer to an end receiver via a relay, and the Response back, through the packet workers of three backends on the loopback address.
// The peer and the end receiver have no connection to each other; each has the relay in its peer list, and the relay has both.
func TestRelayRoundTrip(t *testing.T) {
	testRootPeers(t)

	backendA, networkA := testBackendLoopback(t)
	backendR, networkR := testBackendLoopback(t)
	backendB, networkB := testBackendLoopback(t)

	// connect adds the peer of the other backend to the peer list of the backend.
	connect := func(backend *Backend, network *Network, other *Backend, networkOther *Network) *PeerInfo {
		peer, _ := backend.PeerlistAdd(other.peerPublicKey, &Connection{backend: backend, Network: network, Address: networkOther.address, PortInternal: uint16(networkOther.address.Port)})
		return peer
	}
	relayAtA := connect(backendA, networkA, backendR, networkR)
	connect(backendB, networkB, backendR, networkR)
	connect(backendR, networkR, backendA, networkA)
	connect(backendR, networkR, backendB, networkB)

	requestsAtB := 0
	backendB.Filters.IncomingRequest = func(peer *PeerInfo, RequestType int, Key []byte, Info interface{}) {
		if peer.PublicKey.Equal(backendA.peerPublicKey) && RequestType == protocol.RequestAFindPeer {
			requestsAtB++
		}
	}

	// A sends A_FIND_PEER to B via the relay.
	key, _, _ := ed25519.GenerateKey(rand.Reader)
	virtual := &PeerInfo{Backend: backendA, PublicKey: backendB.peerPublicKey, isVirtual: true, relayPeer: relayAtA}
	info := backendA.nodesDHT.NewInformationRequest(dht.ActionFindNode, key, []*dht.Node{{ID: backendB.peerPublicKey, Info: virtual}})
	virtual.sendAnnouncementFindPeer(info)

	for _, hop := range []struct {
		name    string
		backend *Backend
		network *Network
	}{
		{"Relay message of A at the relay", backendR, networkR},
		{"forwarded Relay message at B", backendB, networkB},
		{"Relay message of B at the relay", backendR, networkR},
		{"forwarded Relay message at A", backendA, networkA},
	} {
		if !testDeliver(hop.backend, hop.network) {
			t.Fatalf("%s: no packet received", hop.name)
		}
	}

	if requestsAtB != 1 {
		t.Errorf("%d A_FIND_PEER requests of A processed at B, expected 1", requestsAtB)
	}

	select {
	case message := <-info.ResultChan:
		if !bytes.Equal(message.SenderID, backendB.peerPublicKey) {
			t.Errorf("result from %x, expected from B", message.SenderID)
		}
	default:
		t.Error("no result of the information request at A")
	}
	if !info.IsTerminated {
		t.Error("the information request at A is not done")
	}

	// A and B are not in the peer list of each other.
	if backendA.PeerlistLookup(backendB.peerPublicKey) != nil || backendB.PeerlistLookup(backendA.peerPublicKey) != nil {
		t.Error("A and B are in the peer list of each other")
	}
}
