/*
File Name:  Network UPnP.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Currently only supports IPv4 networks.
TODO: Limit mapping to X hours. Auto remap upon expiration.
*/

package core

import (
	"log"
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
	// No link-local addresses. 169.254.*.*
	// No loopback.
	if network.address.IP.IsLinkLocalUnicast() || network.address.IP.IsLoopback() {
		return false
	}

	// The network interface must be known which indicates that the IP address is NOT a wildcard.
	// Port forwarding requires to specify a local IP. In case of listening on a wildcard that would not be known and guessing (looking at you btcd) is not a solution.
	if network.iface == nil {
		return false
	}

	return true
}

// upnpAuto runs a UPnP daemon to forward the port, refresh the forwarding and continuously monitor if the forwarding remains valid.
func (network *Network) upnpAuto() {
	if !config.EnableUPnP || !network.upnpIsEligible() {
		return
	}

	// Only allow 1 UPnP worker at a time.
	upnpMutex.Lock()
	defer upnpMutex.Unlock()

	// If there is already a running UPnP on the adapter, skip.
	if _, ok := upnpListInterfaces[network.GetAdapterName()]; ok {
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
		log.Printf("upnpAuto port forwarding invalidated for local IP %s (adapter %s) external IP %s port %d", network.address.String(), network.iface.Name, network.ipExternal.String(), network.portExternal)

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
