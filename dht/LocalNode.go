/*
File Name:  LocalNode.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Wesley Coakley

Represent the local client as an operator on the S/Kademlia network
*/

package dht

import (
	"net"
	"time"

	"github.com/PeerNetOfficial/core"
	"github.com/btcsuite/btcd/btcec"
)

// Period to wait for UDP packet retransmission
// ... the Conversation mechanics should probably be abstracted some more and
// lumped in to PeerNet core but for now we handle this in the Kademlia
// implementation
const DEFAULT_RETRANSMIT_MS = 2000

// The local client as a participant on the Kademlia network
type LocalNode struct {
	// Network connectivity
	Addr *net.UDPAddr // Listening UDP address (needs abstraction)
	Tx []*Conversation // Ongoing *outbound* conversations
	Rx []*Conversation // Ongoing *inbound* conversations

	// Self-Identity
	ID *NodeID // Own public key
	Keys Keyring // Known node operators on the network
	Secretkey *btcec.PrivateKey // Own private key
}

// Keyring helps create a web of trust on the network
type Keyring map[NodeID]KNodeIdentity

// A number of transactions between local client and remote node
type Conversation struct {
	Myself *LocalNode // This end of the socket
	Partner *KNode // The other end of the socket
	Conn *net.UDPConn // Transmission layer itself
	Retransmit *time.Timer // Retransmit on expiration

	RetryCount int // Number of retries
	MsgCount int // # of messages sent / received

	Inbox core.PacketRaw
	Outbox core.PacketRaw

	// IsOutbound bool // Did we initiate this conversation?
}

// Create a new keyring with only the client's own public key on it
//
// This initial key is granted an immediate level of TrustUltimate
// and should be the only key granted this privilige
func (ln *LocalNode) InitializeKeyring() {
	ln.Keys = make(Keyring)
	ln.AddKeyToRing(ln.ID, TrustUltimate)
}

// Add a remote node to the local client's keyring at a given trust level
func (ln *LocalNode) AddKeyToRing(id *NodeID, trust int) {
	pubKey := (*btcec.PublicKey)(id)

	ln.Keys[*id] = KNodeIdentity{
		Pubkey: pubKey,
		Trust: TrustUltimate,
	}
}

// Set up a conversation between the local client and a remote Kademlia node
//
// This helps to manage things like retransmission, and gives a convenient place
// to manage the exchange of large amounts of routing data, e.g. over multiple
// datagrams
func (ln *LocalNode) SetUpConversation(partner *KNode) *Conversation {
	ret := &Conversation{
		Myself: ln,
		Partner: partner,
	}

	// Set up a timer to retransmit after DEFAULT_RETRANSMIT_MS seconds without
	// seeing an ACK from the conversation partner
	ret.Retransmit = time.AfterFunc(
		DEFAULT_RETRANSMIT_MS * time.Millisecond,
		ret.RetransmitLast,
	)

	return ret
}

// Create the network socket between two nodes in a conversation
//
// This is only a stub for now; it should be generalized to use the
// connections we have in PeerNet core
func (convo Conversation) SetUpConn() error {
	conn, err := net.DialUDP("udp", nil, convo.Partner.Addr)

	if err != nil { return err }

	convo.Conn = conn
	return nil
}

// Convert the local client into a "remote" Kademlia node representation
//
// Really only used with test functions which do not connect to the network
// but need to "simulate" a remote connection
func (ln LocalNode) AsKNode() *KNode {
	return &KNode{
		ID: ln.ID,
		Addr: ln.Addr,
	}
}

// Resend our most recent packet (likely the packet was lost)
//
// This should only be called by the retransmission timer. I realize this is an
// inefficient algorithm for retransmission, we could possibly implement
// something like TCP Reno but over an unreliable channel like UDP
// ... a problem for later :)
func (convo Conversation) RetransmitLast() {
	convo.RetryCount = convo.RetryCount + 1
	convo.SendOutbox()
	convo.Retransmit.Reset(DEFAULT_RETRANSMIT_MS * time.Millisecond)
}

// Encrypt a constructed packet using the relevant keys in a Conversation
func (convo Conversation) EncryptOutgoing(packet *core.PacketRaw) ([]byte, error) {
	myPrivkey := convo.Myself.Secretkey
	theirPubkey := convo.Myself.Keys[*convo.Partner.ID].Pubkey

	raw, err := core.PacketEncrypt(myPrivkey, theirPubkey, packet)

	return raw, err
}

// Decrypt a packet using the relevant keys in a Conversation
func (convo Conversation) DecryptIncoming(raw []byte) (*core.PacketRaw, error) {
	theirPubkey := convo.Myself.Keys[*convo.Partner.ID].Pubkey

	decryptedPacket, _, err := core.PacketDecrypt(raw, theirPubkey)

	return decryptedPacket, err
}

// Sends a message pending transmission in the Outbox; this will not remove the
// message from the Outbox, however, that should be done on confirmation /
// receipt of deliver from the remote node
func (convo Conversation) SendOutbox() error {
	raw, err := convo.EncryptOutgoing(&convo.Outbox)

	_, err = convo.Conn.Write(raw)
	if err != nil { return err }

	// if size < ...
	// TODO Actually send the packet along the communication channel

	// Sent packet data
	convo.MsgCount = convo.MsgCount + 1
	convo.Partner.StatsPacketSent = convo.Partner.StatsPacketSent + 1

	return nil
}

