/*
File Name:  Network IPv6 Multicast.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

IPv6 Multicast implementation to support discovery of peers within the same network (Site-local).
Loopback is enabled, which means that Multicast packets sent will be looped back and received by any local listeners. This allows to connect local processes with each other.

Using the separate Multicast port, it allows sending unsolicited announcements without knowing the target's public key. Instead, a hard-coded key is used.

The Multicast listener opens port 12912 with SO_REUSEADDR to allow multiple processes receive the incoming Multicast packets.
[1] mentions "If two sockets are bound to the same interface and port and are members of the same multicast group, data will be delivered to both sockets, rather than an arbitrarily chosen one."

[1] https://docs.microsoft.com/en-us/windows/win32/winsock/using-so-reuseaddr-and-so-exclusiveaddruse
*/

package core

import (
	"encoding/hex"
	"log"
	"net"
	"strconv"
	"time"

	"github.com/PeernetOfficial/core/reuseport"
	"github.com/btcsuite/btcd/btcec"
	"golang.org/x/net/ipv6"
)

// Multicast group is site-local. Group ID is 112.
const ipv6MulticastGroup = "ff05::112"
const ipv6MulticastPort = 12912

// special Public-Private Key pair for local discovery
var ipv6MulticastPrivateKey *btcec.PrivateKey
var ipv6MulticastPublicKey *btcec.PublicKey

const ipv6MulticastPrivateKeyH = "016ad30bfb369926523bf18d136298b6c31d0817e9fb6c21feed89ae22cad788"

func initMulticastIPv6() {
	if configPK, err := hex.DecodeString(ipv6MulticastPrivateKeyH); err == nil {
		ipv6MulticastPrivateKey, ipv6MulticastPublicKey = btcec.PrivKeyFromBytes(btcec.S256(), configPK)
	}
}

// MulticastIPv6Join joins the Multicast group
func (network *Network) MulticastIPv6Join() (err error) {
	if ipv6MulticastPrivateKey == nil || ipv6MulticastPublicKey == nil {
		return
	}

	network.multicastIP = net.ParseIP(ipv6MulticastGroup)

	// listen on a special socket
	network.multicastSocket, err = reuseport.ListenPacket("udp6", net.JoinHostPort(network.address.IP.String(), strconv.Itoa(ipv6MulticastPort)))
	if err != nil {
		return err
	}

	joinMulticastGroup := func(iface *net.Interface) (err error) {
		pc := ipv6.NewPacketConn(network.multicastSocket)
		if err := pc.JoinGroup(iface, &net.UDPAddr{IP: network.multicastIP}); err != nil {
			return err
		}

		// receive messages from self or other processes running on the same computer
		if loop, err := pc.MulticastLoopback(); err == nil {
			if !loop {
				if err := pc.SetMulticastLoopback(true); err != nil {
					log.Printf("MulticastJoin Error setting multicast loopback status: %v\n", err)
				}
			}
		}

		return nil
	}

	// specific interface or join all?
	if network.iface != nil {
		if err = joinMulticastGroup(network.iface); err != nil {
			return err
		}
	} else {
		interfaceList, err := net.Interfaces()
		if err != nil {
			return err
		}

		for _, ifaceSingle := range interfaceList {
			joinMulticastGroup(&ifaceSingle)
		}
	}

	go network.MulticastIPv6Listen()

	return nil
}

// MulticastIPv6Listen listens for incoming multicast packets
// Fork from network.Listen! Keep any changes synced.
func (network *Network) MulticastIPv6Listen() {
	for {
		// Buffer: Must be created for each packet as it is passed as pointer.
		// If the buffer is too small, ReadFromUDP only reads until its length and returns this error: "wsarecvfrom: A message sent on a datagram socket was larger than the internal message buffer or some other network limit, or the buffer used to receive a datagram into was smaller than the datagram itself."
		buffer := make([]byte, maxPacketSize)
		length, sender, err := network.multicastSocket.ReadFrom(buffer)

		if err != nil {
			log.Printf("Listen Error receiving UDP message: %v\n", err) // Only log for debug purposes.
			time.Sleep(time.Millisecond * 50)                           // In case of endless errors, prevent ddos of CPU.
			continue
		}

		// skip incoming packets that were looped back
		if IsAddressSelf(sender.(*net.UDPAddr)) {
			continue
		}

		// For good network practice (and reducing amount of parallel connections), do not allow link-local to talk to non-link-local addresses.
		if sender.(*net.UDPAddr).IP.IsLinkLocalUnicast() != network.address.IP.IsLinkLocalUnicast() {
			continue
		}

		//fmt.Printf("MulticastIPv6Listen from %s at network %s\n", sender.String(), network.address.String())

		if length < packetLengthMin {
			// Discard packets that do not meet the minimum length.
			continue
		}

		// send the packet to a channel which is processed by multiple workers.
		rawPacketsIncoming <- networkWire{network: network, sender: sender.(*net.UDPAddr), raw: buffer[:length], receiverPublicKey: ipv6MulticastPublicKey, unicast: false}
	}
}

// MulticastIPv6Send sends out a single multicast messages to discover peers at the same site
func (network *Network) MulticastIPv6Send() (err error) {
	packets := msgEncodeAnnouncement(true, true, nil, nil, nil)
	if len(packets) == 0 || packets[0].err != nil {
		return packets[0].err
	}

	raw, err := PacketEncrypt(peerPrivateKey, ipv6MulticastPublicKey, &PacketRaw{Protocol: ProtocolVersion, Command: CommandLocalDiscovery, Payload: packets[0].raw})
	if err != nil {
		return err
	}

	// send out the wire
	return network.send(network.multicastIP, ipv6MulticastPort, raw)
}
