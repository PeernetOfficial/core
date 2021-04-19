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
	Network       *Network      // network which received the packet
	Address       *net.UDPAddr  // address of the sender or receiver
	LastPacketIn  time.Time     // Last time an incoming packet was received.
	LastPacketOut time.Time     // Last time an outgoing packet was attempted to send.
	LastPingOut   time.Time     // Last ping out.
	Expires       time.Time     // Inactive connections only: Expiry date. If it does not become active by that date, it will be considered expired and removed.
	Status        int           // 0 = Active established connection, 1 = Inactive, 2 = Removed, 3 = Redundant
	RoundTripTime time.Duration // Full round-trip time of last reply.
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
	return IsIPLocal(c.Address.IP)
}

// IsIPv4 checks if the connection is using IPv4
func (c *Connection) IsIPv4() bool {
	return IsIPv4(c.Address.IP)
}

// IsIPv6 checks if the connection is using IPv6
func (c *Connection) IsIPv6() bool {
	return IsIPv6(c.Address.IP)
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
func (peer *PeerInfo) GetConnection2Share(allowLocal, allowIPv4, allowIPv6 bool) (connections *Connection) {
	peer.RLock()
	defer peer.RUnlock()

	if peer.connectionLatest != nil && !(!allowLocal && peer.connectionLatest.IsLocal()) &&
		(IsIPv4(peer.connectionLatest.Address.IP) && allowIPv4 || IsIPv6(peer.connectionLatest.Address.IP) && allowIPv6) {
		return peer.connectionLatest
	}

	for _, connection := range peer.connectionActive {
		if (IsIPv4(connection.Address.IP) && allowIPv4 || IsIPv6(connection.Address.IP) && allowIPv6) && !(!allowLocal && connection.IsLocal()) {
			return connection
		}
	}

	return nil
}

// registerConnection registers an incoming connection for an existing peer. If new, it will add to the list. If previously inactive, it will elevate.
func (peer *PeerInfo) registerConnection(incoming *Connection) (result *Connection) {
	peer.Lock()
	defer peer.Unlock()

	// first check if already an active connection
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

// ---- sending code ----

// send sends a raw packet to the peer. Only uses active connections.
func (peer *PeerInfo) send(packet *PacketRaw) (err error) {
	if len(peer.connectionActive) == 0 {
		return errors.New("no valid connection to peer")
	}

	packet.Protocol = ProtocolVersion

	raw, err := PacketEncrypt(peerPrivateKey, peer.PublicKey, packet)
	if err != nil {
		return err
	}

	atomic.AddUint64(&peer.StatsPacketSent, 1)

	// Send out the wire. Use connectionLatest if available.
	// Failover: If sending fails and there are other connections available, try those. Automatically update connectionLatest if one is successful.
	// Windows: This works great in case the adapter gets disabled, however, does not detect if the network cable is unplugged.
	c := peer.connectionLatest
	if c != nil {
		c.LastPacketOut = time.Now()

		if err = c.Network.send(c.Address.IP, c.Address.Port, raw); err == nil {
			return nil
		}

		// Invalid connection, immediately invalidate. Fallback to broadcast to all other active ones.
		// Windows: A common error when the network adapter is disabled is "wsasendto: The requested address is not valid in its context".
		if IsNetworkErrorFatal(err) {
			peer.invalidateActiveConnection(c)
		}
	}

	// If no latest connection available, broadcast on all available connections.
	// This might be noisy, but if no latest connection is available it means the last established connection is already considered dead.
	// The receiver is responsible for incoming deduplication of packets.
	activeConnections := peer.GetConnections(true)
	for _, c := range activeConnections {
		c.LastPacketOut = time.Now()
		c.Network.send(c.Address.IP, c.Address.Port, raw)
	}

	return nil // on broadcast no error is known and returned
}

// sendConnection sends a packet to the peer using the specific connection
func (peer *PeerInfo) sendConnection(packet *PacketRaw, connection *Connection) (err error) {
	packet.Protocol = ProtocolVersion
	raw, err := PacketEncrypt(peerPrivateKey, peer.PublicKey, packet)
	if err != nil {
		return err
	}

	atomic.AddUint64(&peer.StatsPacketSent, 1)
	connection.LastPacketOut = time.Now()

	return connection.Network.send(connection.Address.IP, connection.Address.Port, raw)
}

// sendAllNetworks sends a raw packet via all networks
func sendAllNetworks(receiverPublicKey *btcec.PublicKey, packet *PacketRaw, remote *net.UDPAddr) (err error) {
	packet.Protocol = ProtocolVersion
	raw, err := PacketEncrypt(peerPrivateKey, receiverPublicKey, packet)
	if err != nil {
		return err
	}

	successCount := 0

	networksMutex.RLock()
	defer networksMutex.RUnlock()

	if IsIPv6(remote.IP.To16()) {
		for _, network := range networks6 {
			// Do not mix link-local unicast targets with non link-local networks (only when iface is known, i.e. not catch all local)
			if network.iface != nil && remote.IP.IsLinkLocalUnicast() != network.address.IP.IsLinkLocalUnicast() {
				continue
			}

			err = network.send(remote.IP, remote.Port, raw)
			if err == nil {
				successCount++
			}
		}
	} else {
		for _, network := range networks4 {
			// Do not mix link-local unicast targets with non link-local networks (only when iface is known, i.e. not catch all local)
			if network.iface != nil && remote.IP.IsLinkLocalUnicast() != network.address.IP.IsLinkLocalUnicast() {
				continue
			}

			err = network.send(remote.IP, remote.Port, raw)
			if err == nil {
				successCount++
			}
		}
	}

	if successCount == 0 {
		return errors.New("no successful send")
	}

	return nil
}
