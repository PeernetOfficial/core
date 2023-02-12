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
	"sync"
	"time"

	"github.com/PeernetOfficial/core/btcec"
	"github.com/PeernetOfficial/core/protocol"
)

// rootPeer is a single root peer info
type rootPeer struct {
	peer      *PeerInfo        // loaded PeerInfo
	publicKey *btcec.PublicKey // Public key
	addresses []*net.UDPAddr   // IP:Port addresses
	backend   *Backend
}

var rootPeers map[[btcec.PubKeyBytesLenCompressed]byte]*rootPeer

// initSeedList loads the seed list from the config
// Note: This should be called before any network listening function so that incoming root peers are properly recognized.
func (backend *Backend) initSeedList() {
	rootPeers = make(map[[btcec.PubKeyBytesLenCompressed]byte]*rootPeer)
	recentContacts = make(map[[btcec.PubKeyBytesLenCompressed]byte]*recentContactInfo)

loopSeedList:
	for _, seed := range backend.Config.SeedList {
		peer := &rootPeer{backend: backend}

		// parse the Public Key
		publicKeyB, err := hex.DecodeString(seed.PublicKey)
		if err != nil {
			backend.LogError("initSeedList", "public key '%s': %v\n", seed.PublicKey, err.Error())
			continue
		}

		if peer.publicKey, err = btcec.ParsePubKey(publicKeyB, btcec.S256()); err != nil {
			backend.LogError("initSeedList", "public key '%s': %v\n", seed.PublicKey, err.Error())
			continue
		}

		if peer.publicKey.IsEqual(backend.PeerPublicKey) { // skip if self
			continue
		}

		// parse all IP addresses
		for _, addressA := range seed.Address {
			address, err := parseAddress(addressA)
			if err != nil {
				backend.LogError("initSeedList", "public key '%s' address '%s': %v\n", seed.PublicKey, addressA, err.Error())
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
	// If already in peer list, no need to contact.
	if peer.backend.PeerlistLookup(peer.publicKey) != nil {
		return
	}

	for _, address := range peer.addresses {
		// Port internal is always set to 0 for root peers. It disables NAT detection and will not send out a Traverse message.
		peer.backend.contactArbitraryPeer(peer.publicKey, address, 0, false)
	}
}

// bootstrap connects to the initial set of peers.
func (backend *Backend) bootstrap() {
	go resetRecentContacts()

	if len(rootPeers) == 0 {
		backend.LogError("bootstrap", "warning: Empty list of root peers. Connectivity relies on local peer discovery and incoming connections.\n")
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
			} else if peer.peer = peer.backend.PeerlistLookup(peer.publicKey); peer.peer != nil {
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

	backend.LogError("bootstrap", "unable to connect to at least 2 root peers, aborting\n")
}

func (nets *Networks) autoMulticastBroadcast() {
	sendMulticastBroadcast := func() {
		nets.RLock()
		defer nets.RUnlock()

		for _, network := range nets.networks6 {
			if err := network.MulticastIPv6Send(); err != nil {
				nets.backend.LogError("autoMulticastBroadcast", "multicast from network address '%s': %v\n", network.address.IP.String(), err.Error())
			}
		}

		for _, network := range nets.networks4 {
			if err := network.BroadcastIPv4Send(); err != nil {
				nets.backend.LogError("autoMulticastBroadcast", "broadcast from network address '%s': %v\n", network.address.IP.String(), err.Error())
			}
		}
	}

	// Send out multicast/broadcast immediately.
	sendMulticastBroadcast()

	// Phase 1: Resend every 10 seconds until at least 1 peer in the peer list.
	for {
		time.Sleep(time.Second * 10)

		if nets.backend.PeerlistCount() >= 1 {
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

// contactArbitraryPeer contacts a new arbitrary peer for the first time.
func (backend *Backend) contactArbitraryPeer(publicKey *btcec.PublicKey, address *net.UDPAddr, receiverPortInternal uint16, receiverFirewall bool) (contacted bool) {
	findSelf := ShouldSendFindSelf()
	_, blockchainHeight, blockchainVersion := backend.UserBlockchain.Header()
	packets := protocol.EncodeAnnouncement(true, findSelf, nil, nil, nil, backend.FeatureSupport(), blockchainHeight, blockchainVersion, backend.userAgent)
	if len(packets) == 0 {
		return false
	}
	raw := &protocol.PacketRaw{Command: protocol.CommandAnnouncement, Payload: packets[0]}

	backend.Filters.MessageOutAnnouncement(publicKey, nil, raw, findSelf, nil, nil, nil)

	backend.networks.sendAllNetworks(publicKey, raw, address, receiverPortInternal, receiverFirewall, nil, &bootstrapFindSelf{})

	return true
}

// bootstrapFindSelf is a dummy structure assigned to sequences when sending the Announcement message.
// When receiving the Response message, it will know that it was a legitimate bootstrap request.
type bootstrapFindSelf struct {
}

// bootstrapAcceptContacts is the maximum count of contacts considered. It limits the impact of fake peers.
const bootstrapAcceptContacts = 5

// cmdResponseBootstrapFindSelf processes FIND_SELF responses
func (peer *PeerInfo) cmdResponseBootstrapFindSelf(msg *protocol.MessageResponse, closest []protocol.PeerRecord) {
	if len(closest) > bootstrapAcceptContacts {
		closest = closest[:bootstrapAcceptContacts]
	}

	for _, closePeer := range closest {
		if peer.Backend.isReturnedPeerBadQuality(&closePeer) {
			continue
		}

		// If the peer is already in the peer list, no need to contact it again.
		if peer.Backend.PeerlistLookup(closePeer.PublicKey) != nil {
			continue
		}

		// Check if the reported peer was recently contacted (in connection with the origin peer) for bootstrapping. This makes sure inactive peers are not contacted over and over again.
		recent, blacklisted := isReturnedPeerRecent(&closePeer, peer.NodeID)
		if blacklisted {
			continue
		}

		for _, address := range peerRecordToAddresses(&closePeer) {
			// Check if the specific IP:Port was already contacted in the last 5-10 minutes.
			if recent.IsAddressContacted(address) {
				continue
			}

			// Initiate contact. Once a response comes back, the peer will be actually added to the peer list.
			peer.Backend.contactArbitraryPeer(closePeer.PublicKey, &net.UDPAddr{IP: address.IP, Port: int(address.Port)}, address.PortInternal, closePeer.Features&(1<<protocol.FeatureFirewall) > 0)
		}
	}
}

// ShouldSendFindSelf checks if FIND_SELF should be send
func ShouldSendFindSelf() bool {
	// TODO
	return true
}

// isReturnedPeerBadQuality checks if the returned peer record is bad quality and should be discarded
func (backend *Backend) isReturnedPeerBadQuality(record *protocol.PeerRecord) bool {
	isIPv4 := record.IPv4 != nil && !record.IPv4.IsUnspecified()
	isIPv6 := record.IPv6 != nil && !record.IPv6.IsUnspecified()

	// At least one IP must be provided.
	if !isIPv4 && !isIPv6 {
		return true
	}

	// Internal port must be provided. Otherwise the external port is likely not provided either, and checking the NAT and port forwarded status is not possible.
	if isIPv4 && record.IPv4PortReportedInternal == 0 || isIPv6 && record.IPv6PortReportedInternal == 0 {
		//fmt.Printf("IsReturnedPeerBadQuality port internal not available for target %s port %d, peer %s\n", record.IP.String(), record.Port, hex.EncodeToString(record.PublicKey.SerializeCompressed()))
		return true
	}

	// Must not be self. There is no point that a remote peer would return self
	if record.PublicKey.IsEqual(backend.PeerPublicKey) {
		//fmt.Printf("IsReturnedPeerBadQuality received self peer\n")
		return true
	}

	return false
}

// peerRecordToAddresses returns the addresses in a usable way
func peerRecordToAddresses(record *protocol.PeerRecord) (addresses []*peerAddress) {
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

// ---- bootstrap cache of contacted peers to prevent flooding ----

// bootstrapRecentContact is the time in seconds when a peer will not be contacted again for bootstrapping.
// This prevents unnecessary flooding and prevents some attacks. Especially in small networks it will be the case that the same peer is returned multiple times.
const bootstrapRecentContact = 5 * 60 // 5-10 minutes

type recentContactInfo struct {
	added     time.Time           // When the peer was added to the list
	addresses []*peerAddress      // List of contacted addresses in IP:Port format
	origin    map[string]struct{} // List of node IDs who reported this contact
	sync.RWMutex
}

var (
	recentContacts      map[[btcec.PubKeyBytesLenCompressed]byte]*recentContactInfo
	recentContactsMutex sync.RWMutex
)

func resetRecentContacts() {
	for {
		time.Sleep(bootstrapRecentContact * time.Second)
		threshold := time.Now().Add(-bootstrapRecentContact * time.Second)

		recentContactsMutex.Lock()

		for key, recent := range recentContacts {
			if recent.added.Before(threshold) {
				delete(recentContacts, key)
			}
		}

		recentContactsMutex.Unlock()
	}
}

// isReturnedPeerRecent checks if the peer is blacklisted related to the origin peer due to recent contact. It will create a "recent contact" if none exists.
func isReturnedPeerRecent(record *protocol.PeerRecord, originNodeID []byte) (recent *recentContactInfo, blacklisted bool) {
	key := publicKey2Compressed(record.PublicKey)

	recentContactsMutex.Lock()
	defer recentContactsMutex.Unlock()

	if recent = recentContacts[key]; recent == nil {
		recent = &recentContactInfo{added: time.Now(), origin: make(map[string]struct{})}
		recent.origin[string(originNodeID)] = struct{}{}

		recentContacts[key] = recent
	} else {
		if _, blacklisted = recent.origin[string(originNodeID)]; !blacklisted {
			recent.origin[string(originNodeID)] = struct{}{}

			// Here we could add an additional check: If number of recent.addresses (i.e. unique IP:Port tried) exceeds a threshold.
			// However, this is currently not done due to risk of peer isolation. This could happen if enough peers would gang up to report false addresses for a given peer (such peer could still establish an inbound connection to this peer, however).
			// Rather, those peers who report inactive peers should be blacklisted after a given threshold of garbage responses.
		}
	}

	return recent, blacklisted
}

// IsAddressContacted checks if the address was contacted recently
func (recent *recentContactInfo) IsAddressContacted(address *peerAddress) bool {
	recent.Lock()
	defer recent.Unlock()

	for _, addressE := range recent.addresses {
		if addressE.IP.Equal(address.IP) && addressE.Port == address.Port {
			return true
		}
	}

	recent.addresses = append(recent.addresses, address)

	return false
}
