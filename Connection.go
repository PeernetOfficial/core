/*
File Name:  Connection.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"errors"
	"net"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcd/btcec"
)

// Connection is an established connection between a remote IP address and a local network adapter.
// New connections may only be created in case of successful INCOMING packets.
type Connection struct {
	Network       *Network      // Network which received the packet.
	Address       *net.UDPAddr  // Address of the remote peer.
	PortInternal  uint16        // Internal listening port reported by remote peer. 0 if no Announcement/Response message was yet received.
	PortExternal  uint16        // External listening port reported by remote peer. 0 if not known by the peer.
	LastPacketIn  time.Time     // Last time an incoming packet was received.
	LastPacketOut time.Time     // Last time an outgoing packet was attempted to send.
	LastPingOut   time.Time     // Last ping out.
	Expires       time.Time     // Inactive connections only: Expiry date. If it does not become active by that date, it will be considered expired and removed.
	Status        int           // 0 = Active established connection, 1 = Inactive, 2 = Removed, 3 = Redundant
	RoundTripTime time.Duration // Full round-trip time of last reply.
	traversePeer  *PeerInfo     // Temporary peer that may act as proxy for a Traverse message used for the first packet. This is used to establish this Connection to a peer that is behind a NAT.
}

// Connection status
const (
	ConnectionActive = iota
	ConnectionInactive
	ConnectionRemoved
	ConnectionRedundant // Same as active. Incoming packets are accepted. Outgoing use only for redundancy. Reduces ping overhead.
)

// Equal checks if the connection was established other the same network adapter using the same IP address. Port is intentionally not checked.
func (c *Connection) Equal(other *Connection) bool {
	return c.Address.IP.Equal(other.Address.IP) && c.Network.address.IP.Equal(other.Network.address.IP)
}

// IsLocal checks if the connection is a local network one (LAN)
func (c *Connection) IsLocal() bool {
	return c != nil && IsIPLocal(c.Address.IP)
}

// IsIPv4 checks if the connection is using IPv4
func (c *Connection) IsIPv4() bool {
	return IsIPv4(c.Address.IP)
}

// IsIPv6 checks if the connection is using IPv6
func (c *Connection) IsIPv6() bool {
	return IsIPv6(c.Address.IP)
}

// IsBehindNAT checks if the remote peer on the connection is likely behind a NAT
func (c *Connection) IsBehindNAT() bool {
	return c.PortInternal > 0 && c.Address.Port != int(c.PortInternal)
}

// IsPortForward checks if the remote peer uses port forwarding on the connection
func (c *Connection) IsPortForward() bool {
	return c.PortExternal > 0
}

// GetConnections returns the list of connections
func (peer *PeerInfo) GetConnections(active bool) (connections []*Connection) {
	peer.RLock()
	defer peer.RUnlock()

	if active {
		return peer.connectionActive
	}
	return peer.connectionInactive
}

// IsConnectable checks if the peer is connectable to the given IP parameters.
func (peer *PeerInfo) IsConnectable(allowLocal, allowIPv4, allowIPv6 bool) bool {
	peer.RLock()
	defer peer.RUnlock()

	// Only 1 active connection must be allowed for being connectable.
	for _, connection := range peer.connectionActive {
		// If the internal port is not known, which happens if no Announcement or Response was returned, do not share the peer details.
		// This can happen if only other messages such as Ping/Pong were received, or the protocol implementation is not compatible. The external port is also likely not available.
		// In this case sharing the peer would be bad, since the receiving peer could not use internal/external port to detemine the NAT status and port forwarding.
		if connection.PortInternal == 0 {
			continue
		}

		if IsIPv4(connection.Address.IP) && allowIPv4 || IsIPv6(connection.Address.IP) && allowIPv6 {
			if !(!allowLocal && connection.IsLocal()) {
				return true
			}
		}
	}

	return false
}

// GetConnection2Share returns a connection to share. Nil if none.
// allowLocal specifies whether it is OK to return local IPs.
func (peer *PeerInfo) GetConnection2Share(allowLocal, allowIPv4, allowIPv6 bool) (connection *Connection) {
	if !allowLocal && !allowIPv4 && !allowIPv6 {
		return nil
	}

	peer.RLock()
	defer peer.RUnlock()

	if peer.connectionLatest != nil && !(!allowLocal && peer.connectionLatest.IsLocal()) &&
		(IsIPv4(peer.connectionLatest.Address.IP) && allowIPv4 || IsIPv6(peer.connectionLatest.Address.IP) && allowIPv6) && peer.connectionLatest.PortInternal > 0 {
		return peer.connectionLatest
	}

	for _, connection := range peer.connectionActive {
		if (IsIPv4(connection.Address.IP) && allowIPv4 || IsIPv6(connection.Address.IP) && allowIPv6) && !(!allowLocal && connection.IsLocal()) && connection.PortInternal > 0 {
			return connection
		}
	}

	return nil
}

// registerConnection registers an incoming connection for an existing peer. If new, it will add to the list. If previously inactive, it will elevate.
func (peer *PeerInfo) registerConnection(incoming *Connection) (result *Connection) {
	peer.Lock()
	defer peer.Unlock()

	// first check if already an active connection to the same IP
	for _, connection := range peer.connectionActive {
		if connection.Equal(incoming) {
			// Connection already established. Verify port and update if necessary.
			// Some NATs may rotate ports. Some mobile phone providers even rotate IPs which is not detected here.
			if connection.Address.Port != incoming.Address.Port {
				connection.Address.Port = incoming.Address.Port
			}

			connection.Status = ConnectionActive
			peer.setConnectionLatest(connection)
			return connection
		}
	}

	// if an inactive connection, elevate it to activated one
	for n, connection := range peer.connectionInactive {
		if connection.Equal(incoming) {
			if connection.Address.Port != incoming.Address.Port {
				connection.Address.Port = incoming.Address.Port
			}

			// elevate by adding to active and mark as latest active
			connection.Status = ConnectionActive
			peer.connectionActive = append(peer.connectionActive, connection)
			peer.setConnectionLatest(connection)

			// remove from inactive
			inactiveNew := peer.connectionInactive[:n]
			if n < len(peer.connectionInactive)-1 {
				inactiveNew = append(inactiveNew, peer.connectionInactive[n+1:]...)
			}
			peer.connectionInactive = inactiveNew

			return connection
		}
	}

	// otherwise it is a new connection!
	peer.connectionActive = append(peer.connectionActive, incoming)
	peer.setConnectionLatest(incoming)

	Filters.NewPeerConnection(peer, incoming)

	return incoming
}

// setConnectionLatest updates the latest valid connection to use for sending. All other connections will be changed to redundant, which reduces ping overhead.
func (peer *PeerInfo) setConnectionLatest(latest *Connection) {
	if peer.connectionLatest == latest {
		return
	}

	peer.connectionLatest = latest

	for _, connection := range peer.connectionActive {
		if connection == latest {
			continue
		}
		connection.Status = ConnectionRedundant
	}
}

// invalidateActiveConnection invalidates an active connection
func (peer *PeerInfo) invalidateActiveConnection(input *Connection) {
	peer.Lock()
	defer peer.Unlock()

	// Change the status to inactive and start the expiration. If the connection does not become valid by that date, it will be removed.
	input.Status = ConnectionInactive
	input.Expires = time.Now().Add(connectionRemove * time.Second)

	// remove from connectionLatest if selected so it won't be used by standard send function
	if peer.connectionLatest == input {
		peer.connectionLatest = nil
	}

	for n, connection := range peer.connectionActive {
		if connection == input {
			// add to list of inactive connections
			peer.connectionInactive = append(peer.connectionInactive, connection)

			// remove from active
			activeNew := peer.connectionActive[:n]
			if n < len(peer.connectionActive)-1 {
				activeNew = append(activeNew, peer.connectionActive[n+1:]...)
			}
			peer.connectionActive = activeNew

			break
		}
	}
}

// removeInactiveConnection removes an inactive connection.
func (peer *PeerInfo) removeInactiveConnection(input *Connection) {
	peer.Lock()
	defer peer.Unlock()

	input.Status = ConnectionRemoved

	for n, connection := range peer.connectionInactive {
		if connection == input {

			// remove from inactive
			inactiveNew := peer.connectionInactive[:n]
			if n < len(peer.connectionInactive)-1 {
				inactiveNew = append(inactiveNew, peer.connectionInactive[n+1:]...)
			}
			peer.connectionInactive = inactiveNew

			return
		}
	}
}

// GetRTT returns the round-trip time for the most recent active connection. 0 if not available.
func (peer *PeerInfo) GetRTT() (rtt time.Duration) {
	peer.Lock()
	defer peer.Unlock()

	if peer.connectionLatest != nil && peer.connectionLatest.RoundTripTime > 0 {
		return peer.connectionLatest.RoundTripTime
	}

	for _, connection := range peer.connectionActive {
		if connection.RoundTripTime > 0 {
			return connection.RoundTripTime
		}
	}

	return 0
}

// IsBehindNAT checks if the peer is behind NAT
func (peer *PeerInfo) IsBehindNAT() (result bool) {
	peer.Lock()
	defer peer.Unlock()

	// Default is no. Only if a public network reports different connected port vs internal one, NAT is assumed.
	// This also assumes that all 3rd party clients bind their connection to the outgoing port.
	// PortInternal is 0 if no Announcement or Response message was received.

	for _, connection := range peer.connectionActive {
		if connection.IsBehindNAT() {
			return true
		}
	}

	for _, connection := range peer.connectionInactive {
		if connection.IsBehindNAT() {
			return true
		}
	}

	return false
}

// IsPortForward checks if the peer uses port forwarding
func (peer *PeerInfo) IsPortForward() (result bool) {
	peer.Lock()
	defer peer.Unlock()

	for _, connection := range peer.connectionActive {
		if connection.IsPortForward() {
			return true
		}
	}

	for _, connection := range peer.connectionInactive {
		if connection.IsPortForward() {
			return true
		}
	}

	return false
}

// ---- sending code ----

// send sends the packet to the peer on the connection
func (c *Connection) send(packet *PacketRaw, receiverPublicKey *btcec.PublicKey, isFirstPacket bool) (err error) {
	if c == nil {
		return errors.New("invalid connection")
	}

	packet.Protocol = ProtocolVersion
	packet.setSelfReportedPorts(c.Network)

	raw, err := PacketEncrypt(peerPrivateKey, receiverPublicKey, packet)
	if err != nil {
		return err
	}

	c.LastPacketOut = time.Now()

	err = c.Network.send(c.Address.IP, c.Address.Port, raw)

	// Send Traverse message if the peer is behind a NAT and this is the first message. Only for Announcement.
	if err == nil && isFirstPacket && c.IsBehindNAT() && c.traversePeer != nil && packet.Command == CommandAnnouncement {
		c.traversePeer.sendTraverse(packet, receiverPublicKey)
	}

	return err
}

// send sends a raw packet to the peer. Only uses active connections.
func (peer *PeerInfo) send(packet *PacketRaw) (err error) {
	if peer.isVirtual { // special case for peers that were not contacted before
		for _, address := range peer.targetAddresses {
			sendAllNetworks(peer.PublicKey, packet, &net.UDPAddr{IP: address.IP, Port: int(address.Port)}, address.PortInternal, peer.traversePeer, nil)
		}
		return
	}
	if len(peer.connectionActive) == 0 {
		return errors.New("no valid connection to peer")
	}

	// For Traverse: check if no packet has been sent, and none received (i.e. initial contact).
	// If a packet was already received directly (note: not via incoming traversed message), a valid connection is already established.
	isFirstPacketOut := atomic.LoadUint64(&peer.StatsPacketSent) == 0 && atomic.LoadUint64(&peer.StatsPacketReceived) == 0

	// always count as one sent packet even if sent via broadcast
	atomic.AddUint64(&peer.StatsPacketSent, 1)

	// Send out the wire. Use connectionLatest if available.
	// Failover: If sending fails and there are other connections available, try those. Automatically update connectionLatest if one is successful.
	// Windows: This works great in case the adapter gets disabled, however, does not detect if the network cable is unplugged.
	cLatest := peer.connectionLatest
	if cLatest != nil {
		if err := cLatest.send(packet, peer.PublicKey, isFirstPacketOut); err == nil {
			return nil
		} else if IsNetworkErrorFatal(err) {
			// Invalid connection, immediately invalidate. Fallback to broadcast to all other active ones.
			// Windows: A common error when the network adapter is disabled is "wsasendto: The requested address is not valid in its context".
			peer.invalidateActiveConnection(cLatest)
		}
	}

	// If no latest connection available, broadcast on all other available connections.
	// This might be noisy, but if no latest connection is available it means the last established connection is already considered dead.
	// The receiver is responsible for incoming deduplication of packets.
	activeConnections := peer.GetConnections(true)
	for _, c := range activeConnections {
		if c == cLatest {
			continue
		}

		if err := c.send(packet, peer.PublicKey, isFirstPacketOut); err != nil && IsNetworkErrorFatal(err) {
			peer.invalidateActiveConnection(c)
		}
	}

	return nil // on broadcast no error is known and returned
}

// sendConnection sends a packet to the peer using the specific connection
func (peer *PeerInfo) sendConnection(packet *PacketRaw, connection *Connection) (err error) {
	isFirstPacketOut := atomic.LoadUint64(&peer.StatsPacketSent) == 0 && atomic.LoadUint64(&peer.StatsPacketReceived) == 0
	atomic.AddUint64(&peer.StatsPacketSent, 1)

	return connection.send(packet, peer.PublicKey, isFirstPacketOut)
}

// sendAllNetworks sends a raw packet via all networks. It assigns a new sequence for each sent packet.
// receiverPortInternal is important for NAT detection and sending the traverse message.
func sendAllNetworks(receiverPublicKey *btcec.PublicKey, packet *PacketRaw, remote *net.UDPAddr, receiverPortInternal uint16, traversePeer *PeerInfo, sequenceData interface{}) (err error) {
	networksMutex.RLock()
	defer networksMutex.RUnlock()

	networksTarget := networks4
	if IsIPv6(remote.IP.To16()) {
		networksTarget = networks6
	}

	successCount := 0
	isFirstPacket := true

	for _, network := range networksTarget {
		// Do not mix link-local unicast targets with non link-local networks (only when iface is known, i.e. not catch all local)
		if network.iface != nil && remote.IP.IsLinkLocalUnicast() != network.address.IP.IsLinkLocalUnicast() {
			continue
		}

		if sequenceData != nil {
			packet.Sequence = msgArbitrarySequence(receiverPublicKey, sequenceData).sequence
		}
		err = (&Connection{Network: network, Address: remote, PortInternal: receiverPortInternal, traversePeer: traversePeer}).send(packet, receiverPublicKey, isFirstPacket)
		isFirstPacket = false

		if err == nil {
			successCount++
		}
	}

	if successCount == 0 {
		return errors.New("no successful send")
	}

	return nil
}
