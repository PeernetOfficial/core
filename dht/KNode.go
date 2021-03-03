/*
File Name:  KNode.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Wesley Coakley

KNodes ("Kademlia Nodes") are remote nodes on the S/Kademlia network. Each node
is assigned a varying level of trust based on its behavior; good behavior is
rewarded and bad behavior is punished
*/

package dht

import (
	"net"
	"github.com/btcsuite/btcd/btcec"
)

type NodeID btcec.PublicKey

// Some remote node on the network
type KNode struct {
	ID *NodeID // Unique network ID
	Addr *net.UDPAddr // Dial()-able address

	// Statistics
	StatsPacketSent int // Count of packets sent
	StatsPacketReceived int // Count of packets received
}

// Some subset of the total network
type KNodeList []KNode

// Define a node's level of trust
type KNodeIdentity struct {
	Pubkey *btcec.PublicKey // Distinguish public key from node ID for clarity
	Trust int // 0 ~ 4
}

// Equivalent to GPG trust levels for node operators
const (
	TrustUnsure = iota
	TrustNever
	TrustMarginal
	TrustFull
	TrustUltimate
)

