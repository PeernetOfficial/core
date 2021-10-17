/*
File Name:  Network IPv4 Broadcast.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

IPv4 Multicast just sucks (can't use socket bound to 0.0.0.0:PortMain and send to 224.0.0.1:PortMulticast), so we rely on Broadcast instead.
*/

package core

import (
	"encoding/hex"
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/PeernetOfficial/core/protocol"
	"github.com/PeernetOfficial/core/reuseport"
	"github.com/btcsuite/btcd/btcec"
)

const ipv4BroadcastPort = 12912

// special Public-Private Key pair for local discovery
var ipv4BroadcastPrivateKey *btcec.PrivateKey
var ipv4BroadcastPublicKey *btcec.PublicKey

const ipv4BroadcastPrivateKeyH = "5e27ecc8e54a24e71dca9ba84a9bf465400e27b8c46a977d34962d3d88558c8e"

func initBroadcastIPv4() {
	if configPK, err := hex.DecodeString(ipv4BroadcastPrivateKeyH); err == nil {
		ipv4BroadcastPrivateKey, ipv4BroadcastPublicKey = btcec.PrivKeyFromBytes(btcec.S256(), configPK)
	}
}

// BroadcastIPv4 prepares sending Broadcasts
func (network *Network) BroadcastIPv4() (err error) {
	if ipv4BroadcastPrivateKey == nil || ipv4BroadcastPublicKey == nil {
		return
	}

	// listen on a special socket
	network.broadcastSocket, err = reuseport.ListenPacket("udp4", net.JoinHostPort(network.address.IP.String(), strconv.Itoa(ipv4BroadcastPort)))
	if err != nil {
		return err
	}

	network.broadcastIPv4 = networkToIPv4BroadcastIPs(network.ipnet)

	go network.BroadcastIPv4Listen()

	return nil
}

// BroadcastIPv4Listen listens for incoming broadcast packets
// Fork from network.Listen! Keep any changes synced.
func (network *Network) BroadcastIPv4Listen() {
	for {
		// Buffer: Must be created for each packet as it is passed as pointer.
		// If the buffer is too small, ReadFromUDP only reads until its length and returns this error: "wsarecvfrom: A message sent on a datagram socket was larger than the internal message buffer or some other network limit, or the buffer used to receive a datagram into was smaller than the datagram itself."
		buffer := make([]byte, maxPacketSize)
		length, sender, err := network.broadcastSocket.ReadFrom(buffer)

		if err != nil {
			Filters.LogError("BroadcastIPv4Listen", "receiving UDP message: %v\n", err) // Only log for debug purposes.
			time.Sleep(time.Millisecond * 50)                                           // In case of endless errors, prevent ddos of CPU.
			continue
		}

		if networks.ipListen.IsAddressSelf(sender.(*net.UDPAddr)) {
			continue
		}

		// For good network practice (and reducing amount of parallel connections), do not allow link-local to talk to non-link-local addresses.
		if sender.(*net.UDPAddr).IP.IsLinkLocalUnicast() != network.address.IP.IsLinkLocalUnicast() {
			continue
		}

		//fmt.Printf("BroadcastIPv4Listen from %s at network %s\n", sender.String(), network.address.String())

		if length < protocol.PacketLengthMin {
			// Discard packets that do not meet the minimum length.
			continue
		}

		// send the packet to a channel which is processed by multiple workers.
		rawPacketsIncoming <- networkWire{network: network, sender: sender.(*net.UDPAddr), raw: buffer[:length], receiverPublicKey: ipv4BroadcastPublicKey, unicast: false}
	}
}

// BroadcastIPv4Send sends out a single broadcast messages to discover peers
func (network *Network) BroadcastIPv4Send() (err error) {
	_, blockchainHeight, blockchainVersion := UserBlockchain.Header()
	packets := EncodeAnnouncement(true, true, nil, nil, nil, FeatureSupport(), blockchainHeight, blockchainVersion)
	if len(packets) == 0 {
		return errors.New("error encoding broadcast announcement")
	}

	raw, err := protocol.PacketEncrypt(peerPrivateKey, ipv4BroadcastPublicKey, &protocol.PacketRaw{Protocol: ProtocolVersion, Command: protocol.CommandLocalDiscovery, Payload: packets[0]})
	if err != nil {
		return err
	}

	// send out the wire
	for _, ip := range network.broadcastIPv4 {
		err = network.send(ip, ipv4BroadcastPort, raw)
		if err != nil {
			Filters.LogError("BroadcastIPv4Send", "sending UDP packet: %v\n", err)
		}
	}

	return nil
}

// networkToIPv4BroadcastIPs generates the IPv4 addresses to send out the broadcast to
func networkToIPv4BroadcastIPs(ipnet *net.IPNet) (broadcastIPs []net.IP) {
	broadcastIPs = append(broadcastIPs, net.IPv4bcast)

	if ipnet != nil {
		if ip2 := ipv4DirectedBroadcast(ipnet); ip2 != nil {
			broadcastIPs = append(broadcastIPs, ip2)
		}
	} else {
		interfaceList, err := net.Interfaces()
		if err != nil {
			return
		}

		for _, iface := range interfaceList {
			addresses, err := iface.Addrs()
			if err != nil {
				continue
			}

			for _, address := range addresses {
				net1 := address.(*net.IPNet)

				// TODO: Does the rfc3927Net make sense?
				if !IsIPv4(net1.IP) || rfc3927Net.Contains(net1.IP) {
					continue
				}

				if ip2 := ipv4DirectedBroadcast(net1); ip2 != nil {
					broadcastIPs = append(broadcastIPs, ip2)
				}
			}
		}
	}

	// TODO: Result could contain duplicates, filter them out

	return broadcastIPs
}

func ipv4DirectedBroadcast(n *net.IPNet) net.IP {
	ip4 := n.IP.To4()
	if ip4 == nil {
		return nil
	}
	last := make(net.IP, len(ip4))
	copy(last, ip4)
	for i := range ip4 {
		last[i] |= ^n.Mask[i]
	}
	return last
}

var (
	// rfc3927Net specifies the IPv4 auto configuration address block as
	// defined by RFC3927 (169.254.0.0/16).
	rfc3927Net = ipNet("169.254.0.0", 16, 32)
)

// ipNet returns a net.IPNet struct given the passed IP address string, number
// of one bits to include at the start of the mask, and the total number of bits
// for the mask.
func ipNet(ip string, ones, bits int) net.IPNet {
	return net.IPNet{IP: net.ParseIP(ip), Mask: net.CIDRMask(ones, bits)}
}
