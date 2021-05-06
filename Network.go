/*
File Name:  Network.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"errors"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PeernetOfficial/core/upnp"
)

// Network is a connection adapter through one network interface (adapter).
// Note that for each IP on the same adapter separate network entries are created.
type Network struct {
	iface           *net.Interface   // Network interface belonging to the IP. May not be set.
	ipnet           *net.IPNet       // IP network the listening address belongs to. May not be set.
	address         *net.UDPAddr     // IP:Port where the server listens
	socket          *net.UDPConn     // active socket for send/receive
	multicastIP     net.IP           // Multicast IP, IPv6 only.
	multicastSocket net.PacketConn   // Multicast socket, IPv6 only.
	broadcastSocket net.PacketConn   // Broadcast socket, IPv4 only.
	broadcastIPv4   []net.IP         // Broadcast IPs, IPv4 only.
	portExternal    uint16           // External port. 0 if not known.
	ipExternal      net.IP           // External IP of the network. Usually not known.
	nat             upnp.NAT         // UPnP: NAT information
	isTerminated    bool             // If true, the network was signaled for termination
	terminateSignal chan interface{} // gets closed on termination signal, can be used in select via "case _ = <- network.terminateSignal:"
	sync.RWMutex                     // for sychronized closing
}

// networks is a list of all connected networks
var networks4, networks6 []*Network

// single mutex for both network lists. Higher granularity currently not needed.
var networksMutex sync.RWMutex

// Default ports to use. This may be randomized in the future to prevent fingerprinting (and subsequent blocking) by corporate and ISP firewalls.
const defaultPort = 'p' // 112

// ReplyTimeout is the round-trip timeout.
var ReplyTimeout = 20

// AutoAssignPort assigns a port for the given IP. Use port 0 for zero configuration.
func (network *Network) AutoAssignPort(ip net.IP, port int) (err error) {
	networkA := "udp6"
	if IsIPv4(ip) {
		networkA = "udp4"
	}

	// A common error return is "bind: The requested address is not valid in its context.".
	// This error was observed when the network interface might not be ready after boot but also when listening on a link-local IPv4 (169.254.) for an inactive adapter.
	// Previously the algorithm retried up to n times, but this would unnecessarily delay startup in case the IP is actual unlistenable.
	connectPortTry := func(port int) (address *net.UDPAddr, socket *net.UDPConn, err error) {
		address = &net.UDPAddr{IP: ip, Port: port}
		if socket, err = net.ListenUDP(networkA, address); err != nil {
			return nil, nil, err
		}

		if port == 0 {
			localAddr := socket.LocalAddr()
			if localAddr == nil {
				return nil, nil, errors.New("invalid port assignment")
			}
			address.Port = localAddr.(*net.UDPAddr).Port
		}

		return address, socket, nil
	}

	if port != 0 {
		network.address, network.socket, err = connectPortTry(port)
		return err
	}

	// try default main port, then random
	if network.address, network.socket, err = connectPortTry(defaultPort); err == nil {
		return nil
	}

	if network.address, network.socket, err = connectPortTry(0); err == nil {
		return nil
	}

	return err
}

// send sends a message
func (network *Network) send(IP net.IP, port int, raw []byte) (err error) {
	_, err = network.socket.WriteTo(raw, &net.UDPAddr{IP: IP, Port: port})
	return err
}

// Currently packets are maxed at 4 KB. This is going to be refined.
const maxPacketSize = 4096

// Listen starts listening for incoming packets on the given UDP connection
func (network *Network) Listen() {
	for !network.isTerminated {
		// Buffer: Must be created for each packet as it is passed as pointer.
		// If the buffer is too small, ReadFromUDP only reads until its length and returns this error: "wsarecvfrom: A message sent on a datagram socket was larger than the internal message buffer or some other network limit, or the buffer used to receive a datagram into was smaller than the datagram itself."
		buffer := make([]byte, maxPacketSize)
		length, sender, err := network.socket.ReadFromUDP(buffer)

		if err != nil {
			// Exit on closed socket. Error will be "use of closed network connection".
			if network.isTerminated {
				return
			}

			log.Printf("Listen Error receiving UDP message: %v\n", err) // Only log for debug purposes.
			time.Sleep(time.Millisecond * 50)                           // In case of endless errors, prevent ddos of CPU.
			continue
		}

		if length < packetLengthMin {
			// Discard packets that do not meet the minimum length.
			continue
		}

		// send the packet to a channel which is processed by multiple workers.
		rawPacketsIncoming <- networkWire{network: network, sender: sender, raw: buffer[:length], receiverPublicKey: peerPublicKey, unicast: true}
	}
}

// packetWorker handles incoming packets.
func packetWorker(packets <-chan networkWire) {
	for packet := range packets {
		decoded, senderPublicKey, err := PacketDecrypt(packet.raw, packet.receiverPublicKey)
		if err != nil {
			//log.Printf("packetWorker Error decrypting packet from '%s': %s\n", packet.sender.String(), err.Error())
			continue
		}

		// immediately discard message if sender = self
		if senderPublicKey.IsEqual(peerPublicKey) {
			continue
		}

		// supported protocol version
		if decoded.Protocol != 0 {
			continue
		}

		connection := &Connection{Network: packet.network, Address: packet.sender, Status: ConnectionActive}

		// A peer structure will always be returned, even if the peer won't be added to the peer list.
		peer, added := PeerlistAdd(senderPublicKey, connection)
		if !added {
			connection = peer.registerConnection(connection)
		}

		atomic.AddUint64(&peer.StatsPacketReceived, 1)
		connection.LastPacketIn = time.Now()

		// process the packet
		raw := &MessageRaw{SenderPublicKey: senderPublicKey, PacketRaw: *decoded, connection: connection}

		switch decoded.Command {
		case CommandAnnouncement: // Announce
			if announce, _ := msgDecodeAnnouncement(raw); announce != nil {
				// Update known internal/external port and User Agent
				connection.PortInternal = announce.PortInternal
				connection.PortExternal = announce.PortExternal
				if len(announce.UserAgent) > 0 {
					peer.UserAgent = announce.UserAgent
				}

				peer.cmdAnouncement(announce)
			}

		case CommandResponse: // Response
			if response, _ := msgDecodeResponse(raw); response != nil {
				// Validate sequence number which prevents unsolicited responses.
				if valid, rtt := msgValidateSequence(raw, response.Actions&(1<<ActionSequenceLast) > 0); !valid {
					log.Printf("packetWorker message with invalid sequence %d command %d from %s\n", raw.Sequence, raw.Command, raw.connection.Address.String()) // Only log for debug purposes.
					continue
				} else if rtt > 0 {
					connection.RoundTripTime = rtt
				}

				// Update known internal/external port and User Agent
				connection.PortInternal = response.PortInternal
				connection.PortExternal = response.PortExternal
				if len(response.UserAgent) > 0 {
					peer.UserAgent = response.UserAgent
				}

				peer.cmdResponse(response)
			}

		case CommandLocalDiscovery: // Local discovery, sent via IPv4 broadcast and IPv6 multicast
			if announce, _ := msgDecodeAnnouncement(raw); announce != nil {
				if len(announce.UserAgent) > 0 {
					peer.UserAgent = announce.UserAgent
				}

				peer.cmdLocalDiscovery(announce)
			}

		case CommandPing: // Ping
			peer.cmdPing(raw)

		case CommandPong: // Ping
			// Validate sequence number which prevents unsolicited responses.
			if valid, rtt := msgValidateSequence(raw, true); !valid {
				log.Printf("packetWorker message with invalid sequence %d command %d from %s\n", raw.Sequence, raw.Command, raw.connection.Address.String()) // Only log for debug purposes.
				continue
			} else if rtt > 0 {
				connection.RoundTripTime = rtt
			}

			peer.cmdPong(raw)

		case CommandChat: // Chat [debug]
			peer.cmdChat(raw)

		default: // Unknown command

		}

	}
}

// GetNetworks returns the list of connected networks
func GetNetworks(networkType int) (networks []*Network) {
	switch networkType {
	case 4:
		return networks4
	case 6:
		return networks6
	}
	return nil
}

// GetListen returns connectivity information
func (network *Network) GetListen() (listen *net.UDPAddr, multicastIPv6 net.IP, broadcastIPv4 []net.IP, ipExternal net.IP, portExternal uint16) {
	return network.address, network.multicastIP, network.broadcastIPv4, network.ipExternal, network.portExternal
}

// GetAdapterName returns the adapter name, if available
func (network *Network) GetAdapterName() string {
	if network.iface != nil {
		return network.iface.Name
	}
	return "[unknown adapter]"
}

// Terminate sends the termination signal to all workers. It is safe to call Terminate multiple times.
func (network *Network) Terminate() {
	network.Lock()
	defer network.Unlock()

	if network.isTerminated {
		return
	}

	// set the termination signal
	network.isTerminated = true
	close(network.terminateSignal) // safety guaranteed via lock
	network.socket.Close()         // Will stop the listener from blocking on network.socket.ReadFromUDP

	removeListenAddress(network.address)
}
