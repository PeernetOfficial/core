/*
File Name:  Network UPnP.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Currently only supports IPv4 networks.
*/

package core

import (
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/PeernetOfficial/core/upnp"
)

var upnpListInterfaces map[string]struct{}
var upnpMutex sync.RWMutex

func startUPnP() {
	upnpListInterfaces = make(map[string]struct{})

	for _, cidr := range []string{
		"10.0.0.0/8",     // RFC1918
		"172.16.0.0/12",  // RFC1918
		"192.168.0.0/16", // RFC1918
	} {
		if _, block, err := net.ParseCIDR(cidr); err == nil {
			privateIPv4Blocks = append(privateIPv4Blocks, block)
		}
	}

	if config.PortForward > 0 {
		config.EnableUPnP = false
	}
	if !config.EnableUPnP {
		return
	}

	for _, network := range networks4 {
		go network.upnpAuto()
	}
}

// upnpIsEligible checks if the network is eligible for UPnP
func (network *Network) upnpIsEligible() bool {
	// IPv4 only for now.
	if !IsIPv4(network.address.IP) {
		return false
	}

	// The network interface must be known which indicates that the IP address is NOT a wildcard.
	// Port forwarding requires to specify a local IP. In case of listening on a wildcard that would not be known and guessing (looking at you btcd) is not a solution.
	if network.iface == nil || network.address.IP.IsUnspecified() {
		return false
	}

	// IPv4/IPv6: No link-local addresses, no loopback. Multicast would be invalid anyway.
	if network.address.IP.IsLinkLocalUnicast() || network.address.IP.IsLoopback() || network.address.IP.IsMulticast() {
		return false
	}

	// IPv4: Must be private IP.
	if IsIPv4(network.address.IP) && !isPrivateIP(network.address.IP) {
		return false
	}

	return true
}

var privateIPv4Blocks []*net.IPNet

func isPrivateIP(ip net.IP) bool {
	for _, block := range privateIPv4Blocks {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// upnpAuto runs a UPnP daemon to forward the port, refresh the forwarding and continuously monitor if the forwarding remains valid.
func (network *Network) upnpAuto() {
	if !config.EnableUPnP || !network.upnpIsEligible() {
		return
	}

	nat, err := upnp.Discover(network.address.IP)
	if err != nil {
		return
	}

	network.nat = nat

	externalIP, err := nat.GetExternalAddress()
	if err != nil {
		return
	}

	network.ipExternal = externalIP

	// Only allow 1 UPnP worker at a time for registering the adapter.
	upnpMutex.Lock()
	defer upnpMutex.Unlock()

	// If there is already a running UPnP on the adapter, skip.
	if _, ok := upnpListInterfaces[network.GetAdapterName()]; ok {
		return
	}

	if err := network.upnpTryPortForward(); err != nil {
		return
	}

	upnpListInterfaces[network.GetAdapterName()] = struct{}{}

	go network.upnpMonitorPortForward()
}

// upnpMonitorPortForward monitors the port forwarding status
func (network *Network) upnpMonitorPortForward() {
	ticker := time.NewTicker(time.Second * 10)

monitorLoop:
	for {
		select {
		case <-ticker.C:
		case <-network.terminateSignal:
			// Remove port mapping. Note that in case the network is unavailable this is likely to fail.
			network.nat.DeletePortMapping("UDP", network.portExternal)

			network.portExternal = 0
			network.ipExternal = net.IP{}

			break monitorLoop
		}

		// 3 tries
		var err error
		for n := 0; n < 3; n++ {
			if err = network.upnpValidate(); err == nil {
				continue monitorLoop
			}
		}

		// invalid :(
		Filters.LogError("upnpMonitorPortForward", "port forwarding invalidated for local IP %s (adapter %s) external IP %s port %d", network.address.String(), network.iface.Name, network.ipExternal.String(), network.portExternal)

		network.portExternal = 0
		network.ipExternal = net.IP{}

		break
	}

	ticker.Stop()

	upnpMutex.Lock()
	delete(upnpListInterfaces, network.GetAdapterName())
	upnpMutex.Unlock()
}

func (network *Network) upnpTryPortForward() (err error) {
	// Try forwarding the port. First to the same one listening, otherwise random.
	mappedExternalPort, err := network.nat.AddPortMapping("UDP", network.address.IP, uint16(network.address.Port), uint16(network.address.Port), "Peernet", 0)
	if err != nil {
		mappedExternalPort, err = network.nat.AddPortMapping("UDP", network.address.IP, uint16(network.address.Port), uint16(randInt(1024, 65535)), "Peernet", 0)
	}
	if err != nil {
		return err
	}

	// validate
	if err := network.upnpValidate(); err != nil {
		return err
	}

	// valid!
	network.portExternal = mappedExternalPort

	return nil
}

func randInt(min int, max int) int {
	return min + rand.Intn(max-min)
}

func (network *Network) upnpValidate() (err error) {
	// TODO: Send special message which validates the UPnP mapping
	return nil
}

// TODO: Function to check if there is an existing port forward
