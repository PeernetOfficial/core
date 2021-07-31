/*
File Name:  Bootstrap.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Strategy for sending our IPv6 Multicast and IPv4 Broadcast messages:
* During bootstrap: Immediately at the beginning, then every 10 seconds until there is at least 1 peer.
* Every 10 minutes during regular operation.
* Each time a network adapter / IP change is detected.

*/

package core

import (
	"encoding/hex"
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/btcsuite/btcd/btcec"
)

// rootPeer is a single root peer info
type rootPeer struct {
	peer      *PeerInfo        // loaded PeerInfo
	publicKey *btcec.PublicKey // Public key
	addresses []*net.UDPAddr   // IP:Port addresses
}

var rootPeers map[[btcec.PubKeyBytesLenCompressed]byte]*rootPeer

// initSeedList loads the seed list from the config
// Note: This should be called before any network listening function so that incoming root peers are properly recognized.
func initSeedList() {
	rootPeers = make(map[[btcec.PubKeyBytesLenCompressed]byte]*rootPeer)

loopSeedList:
	for _, seed := range config.SeedList {
		peer := &rootPeer{}

		// parse the Public Key
		publicKeyB, err := hex.DecodeString(seed.PublicKey)
		if err != nil {
			Filters.LogError("initSeedList", "public key '%s': %v\n", seed.PublicKey, err.Error())
			continue
		}

		if peer.publicKey, err = btcec.ParsePubKey(publicKeyB, btcec.S256()); err != nil {
			Filters.LogError("initSeedList", "public key '%s': %v\n", seed.PublicKey, err.Error())
			continue
		}

		if peer.publicKey.IsEqual(peerPublicKey) { // skip if self
			continue
		}

		// parse all IP addresses
		for _, addressA := range seed.Address {
			address, err := parseAddress(addressA)
			if err != nil {
				Filters.LogError("initSeedList", "public key '%s' address '%s': %v\n", seed.PublicKey, addressA, err.Error())
				continue loopSeedList
			}

			peer.addresses = append(peer.addresses, address)
		}

		rootPeers[publicKey2Compressed(peer.publicKey)] = peer
	}
}

// parseAddress parses an input peer address in the form "IP:Port".
func parseAddress(Address string) (remote *net.UDPAddr, err error) {
	host, portA, err := net.SplitHostPort(Address)
	if err != nil {
		return nil, err
	}

	portI, err := strconv.Atoi(portA)
	if err != nil {
		return nil, err
	} else if portI <= 0 || portI > 65535 {
		return nil, errors.New("invalid port number")
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return nil, errors.New("invalid input IP")
	}

	return &net.UDPAddr{IP: ip, Port: portI}, err
}

// contact tries to contact the root peer on all networks
func (peer *rootPeer) contact() {
	for _, address := range peer.addresses {
		// Port internal is always set to 0 for root peers. It disables NAT detection and will not send out a Traverse message.
		contactArbitraryPeer(peer.publicKey, address, 0)
	}
}

// bootstrap connects to the initial set of peers.
func bootstrap() {
	if len(rootPeers) == 0 {
		Filters.LogError("bootstrap", "warning: Empty list of root peers. Connectivity relies on local peer discovery and incoming connections.\n")
		return
	}

	contactRootPeers := func() {
		for _, peer := range rootPeers {
			if peer.peer == nil {
				peer.contact()
			}
		}
	}

	countConnectedRootPeers := func() (connectedCount, total int) {
		for _, peer := range rootPeers {
			if peer.peer != nil {
				connectedCount++
			} else if peer.peer = PeerlistLookup(peer.publicKey); peer.peer != nil {
				connectedCount++
			}
		}
		return connectedCount, len(rootPeers)
	}

	// initial contact to all root peer
	contactRootPeers()

	// Phase 1: First 10 minutes. Try every 7 seconds to connect to all root peers until at least 2 peers connected.
	for n := 0; n < 10*60/7; n++ {
		time.Sleep(time.Second * 7)

		if connected, total := countConnectedRootPeers(); connected == total || connected >= 2 {
			return
		}

		contactRootPeers()
	}

	// Phase 2: After that (if not 2 peers), try every 5 minutes to connect to remaining root peers for a maximum of 1 hour.
	for n := 0; n < 1*60/5; n++ {
		time.Sleep(time.Minute * 5)

		contactRootPeers()

		if connected, total := countConnectedRootPeers(); connected == total || connected >= 2 {
			return
		}
	}

	Filters.LogError("bootstrap", "unable to connect to at least 2 root peers, aborting\n")
}

func autoMulticastBroadcast() {
	sendMulticastBroadcast := func() {
		networksMutex.RLock()
		defer networksMutex.RUnlock()

		for _, network := range networks6 {
			if err := network.MulticastIPv6Send(); err != nil {
				Filters.LogError("autoMulticastBroadcast", "multicast from network address '%s': %v\n", network.address.IP.String(), err.Error())
			}
		}

		for _, network := range networks4 {
			if err := network.BroadcastIPv4Send(); err != nil {
				Filters.LogError("autoMulticastBroadcast", "broadcast from network address '%s': %v\n", network.address.IP.String(), err.Error())
			}
		}
	}

	// Send out multicast/broadcast immediately.
	sendMulticastBroadcast()

	// Phase 1: Resend every 10 seconds until at least 1 peer in the peer list.
	for {
		time.Sleep(time.Second * 10)

		if PeerlistCount() >= 1 {
			break
		}

		sendMulticastBroadcast()
	}

	// Phase 2: Every 10 minutes.
	for {
		time.Sleep(time.Minute * 10)
		sendMulticastBroadcast()
	}
}

// contactArbitraryPeer reaches for the first time to an arbitrary peer.
// It does not contact the peer if it is in the peer list, which means that a connection is already established.
func contactArbitraryPeer(publicKey *btcec.PublicKey, address *net.UDPAddr, receiverPortInternal uint16) (contacted bool) {
	if peer := PeerlistLookup(publicKey); peer != nil {
		return false
	}

	findSelf := ShouldSendFindSelf()
	packets := msgEncodeAnnouncement(true, findSelf, nil, nil, nil)
	if len(packets) == 0 || packets[0].err != nil {
		return false
	}
	raw := &PacketRaw{Command: CommandAnnouncement, Payload: packets[0].raw}

	Filters.MessageOutAnnouncement(publicKey, nil, raw, findSelf, nil, nil, nil)

	sendAllNetworks(publicKey, raw, address, receiverPortInternal, nil, &bootstrapFindSelf{})

	return true
}

// bootstrapFindSelf is a dummy structure assigned to sequences when sending the Announcement message.
// When receiving the Response message, it will know that it was a legitimate bootstrap request.
type bootstrapFindSelf struct {
}

// bootstrapAcceptContacts is the maximum count of contacts considered. It limits the impact of fake peers.
const bootstrapAcceptContacts = 5

// cmdResponseBootstrapFindSelf processes FIND_SELF responses
func (peer *PeerInfo) cmdResponseBootstrapFindSelf(msg *MessageResponse, closest []PeerRecord) {
	if len(closest) > bootstrapAcceptContacts {
		closest = closest[:bootstrapAcceptContacts]
	}

	for _, closePeer := range closest {
		if closePeer.IsBadQuality() {
			continue
		}

		for _, address := range closePeer.ToAddresses() {
			// Initiate contact. Once a response comes back, the peer will be actually added to the peer list.
			if contactArbitraryPeer(closePeer.PublicKey, &net.UDPAddr{IP: address.IP, Port: int(address.Port)}, address.PortInternal) {
				// Blacklist the target Peer ID, IP:Port for contact in the next 10 minutes.
				// TODO
			}
		}
	}
}

// ShouldSendFindSelf checks if FIND_SELF should be send
func ShouldSendFindSelf() bool {
	// TODO
	return true
}

// IsBadQuality checks if the returned peer record is bad quality and should be discarded
func (record *PeerRecord) IsBadQuality() bool {
	isIPv4 := record.IPv4 != nil && !record.IPv4.IsUnspecified()
	isIPv6 := record.IPv6 != nil && !record.IPv6.IsUnspecified()

	// At least one IP must be provided.
	if !isIPv4 && !isIPv6 {
		return true
	}

	// Internal port must be provided. Otherwise the external port is likely not provided either, and checking the NAT and port forwarded status is not possible.
	if isIPv4 && record.IPv4PortReportedInternal == 0 || isIPv6 && record.IPv6PortReportedInternal == 0 {
		//fmt.Printf("IsBadQuality port internal not available for target %s port %d, peer %s\n", record.IP.String(), record.Port, hex.EncodeToString(record.PublicKey.SerializeCompressed()))
		return true
	}

	// Must not be self. There is no point that a remote peer would return self
	if record.PublicKey.IsEqual(peerPublicKey) {
		//fmt.Printf("IsBadQuality received self peer\n")
		return true
	}

	return false
}

// ToAddresses returns the addresses in a usable way
func (record *PeerRecord) ToAddresses() (addresses []*peerAddress) {
	// IPv4
	ipv4Port := record.IPv4Port
	if record.IPv4PortReportedExternal > 0 { // Use the external port if available
		ipv4Port = record.IPv4PortReportedExternal
	}
	if record.IPv4 != nil && !record.IPv4.IsUnspecified() {
		addresses = append(addresses, &peerAddress{IP: record.IPv4, Port: ipv4Port, PortInternal: record.IPv4PortReportedInternal})
	}

	// IPv6
	ipv6Port := record.IPv6Port
	if record.IPv6PortReportedExternal > 0 { // Use the external port if available
		ipv6Port = record.IPv6PortReportedExternal
	}
	if record.IPv6 != nil && !record.IPv6.IsUnspecified() {
		addresses = append(addresses, &peerAddress{IP: record.IPv6, Port: ipv6Port, PortInternal: record.IPv6PortReportedInternal})
	}

	return addresses
}
