/*
File Name:  Network Init.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Magic 🪄 to start the network configuration with 0 manual input. Users may specify the list of IPs (and optional ports) to listen; otherwise it listens on all.
IPv6 is always preferred.
*/

package core

import (
	"errors"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PeernetOfficial/core/btcec"
)

// networkWire is an incoming packet
type networkWire struct {
	network           *Network         // network which received the packet
	sender            *net.UDPAddr     // sender of the packet
	receiverPublicKey *btcec.PublicKey // public key associated with the receiver
	raw               []byte           // buffer
	unicast           bool             // True if the message was sent via unicast. False if sent via IPv4 broadcast or IPv6 multicast.
}

// initNetwork sets up the network configuration and starts listening.
func (backend *Backend) initNetwork() {
	rand.Seed(time.Now().UnixNano()) // we are not using "crypto/rand" for speed tradeoff

	// start listen workers
	if backend.Config.ListenWorkers == 0 {
		backend.Config.ListenWorkers = 2
	}
	if backend.Config.ListenWorkersLite == 0 {
		backend.Config.ListenWorkersLite = 2
	}
	for n := 0; n < backend.Config.ListenWorkers; n++ {
		go backend.networks.packetWorker()
	}
	for n := 0; n < backend.Config.ListenWorkersLite; n++ {
		go backend.networks.packetWorkerLite()
	}

	// check if user specified where to listen
	if len(backend.Config.Listen) > 0 {
		for _, listenA := range backend.Config.Listen {
			host, portA, err := net.SplitHostPort(listenA)
			if err != nil && strings.Contains(err.Error(), "missing port in address") { // port is optional
				host = listenA
				portA = "0"
			} else if err != nil {
				backend.LogError("initNetwork", "invalid input listen address '%s': %s\n", listenA, err.Error())
				continue
			}

			portI, _ := strconv.Atoi(portA)

			if _, err := backend.networks.PrepareListen(host, portI); err != nil {
				backend.LogError("initNetwork", "listen on '%s': %s\n", listenA, err.Error())
				continue
			}
		}

		return
	}

	// Listen on all IPv4 and IPv6 addresses
	//if _, err := networks.PrepareListen("0.0.0.0", 0); err != nil {
	//	LogError("initNetwork", "listen on all IPv4 addresses (0.0.0.0): %s\n", err.Error())
	//}
	//if _, err := networks.PrepareListen("::", 0); err != nil {
	//	LogError("initNetwork", "listen on all IPv6 addresses (::): %s\n", err.Error())
	//}

	// Listen on each network adapter on each IP. This guarantees the highest deliverability, even though it brings on additional challenges such as:
	// * Packet duplicates on IPv6 Multicast (listening on multiple IPs and joining the group on the same adapter) and IPv4 Broadcast (listening on multiple IPs on the same adapter).
	// * Local peers are more likely to connect on the same adapter via multiple IPs (i.e. link-local and others, including public IPv6 and temporary public IPv6).
	// * Network adapters and IPs might change. Simplest case is if someone changes Wifi network.
	interfaceList, err := net.Interfaces()
	if err != nil {
		backend.LogError("initNetwork", "enumerating network adapters failed: %s\n", err.Error())
		return
	}

	for _, iface := range interfaceList {
		addresses, err := iface.Addrs()
		if err != nil {
			backend.LogError("initNetwork", "enumerating IPs for network adapter '%s': %s\n", iface.Name, err.Error())
			continue
		}

		backend.networks.ipListen.ifacesExist[iface.Name] = addresses

		backend.networks.InterfaceStart(iface, addresses)
	}
}

// InterfaceStart will start the listeners on all the IP addresses for the network
func (nets *Networks) InterfaceStart(iface net.Interface, addresses []net.Addr) (networksNew []*Network) {
	for _, address := range addresses {
		net1 := address.(*net.IPNet)

		// Do not listen on lookpback IPs. They are not even needed for discovery of machine-local peers (they will be discovered via regular multicast/broadcast).
		if net1.IP.IsLoopback() {
			continue
		}

		networkNew, err := nets.PrepareListen(net1.IP.String(), 0)

		if err != nil {
			// Do not log common errors:
			// * "listen udp4 169.254.X.X:X: bind: The requested address is not valid in its context."
			// Windows reports link-local addresses for inactive network adapters.
			if net1.IP.IsLinkLocalUnicast() {
				continue
			}

			nets.backend.LogError("networks.InterfaceStart", "listening on network adapter '%s' IPv4 '%s': %s\n", iface.Name, net1.IP.String(), err.Error())
			continue
		}

		nets.ipListen.Add(networkNew.address)

		nets.backend.LogError("networks.InterfaceStart", "listen on network '%s' UDP %s\n", iface.Name, networkNew.address.String())

		networksNew = append(networksNew, networkNew)
	}

	return
}

// PrepareListen creates a new network and prepares to listen on the given IP address. If port is 0, one is chosen automatically.
func (nets *Networks) PrepareListen(ipA string, port int) (network *Network, err error) {
	ip := net.ParseIP(ipA)
	if ip == nil {
		return nil, errors.New("invalid input IP")
	}

	network = &Network{backend: nets.backend, networkGroup: nets}
	network.terminateSignal = make(chan interface{})

	// get the network interface that belongs to the IP
	if !ip.IsUnspecified() { // checks for IPv4 "0.0.0.0" and IPv6 "::"
		network.iface, network.ipnet = FindInterfaceByIP(ip)
		if network.iface == nil {
			return nil, errors.New("error finding the network interface belonging to IP")
		}
	}

	// open up the port
	if err = network.AutoAssignPort(ip, port); err != nil {
		return nil, err
	}

	nets.Lock()

	// Success - port is open. Add to the list and start accepting incoming messages.
	if IsIPv4(ip) {
		nets.networks4 = append(nets.networks4, network)
		nets.Unlock()
		network.BroadcastIPv4()
	} else {
		nets.networks6 = append(nets.networks6, network)
		nets.Unlock()
		network.MulticastIPv6Join()
	}

	go network.Listen()

	return network, nil
}

// ipList keeps track of listened IP addresses and observed interfaces
type ipList struct {
	ipListen     map[string]struct{}   // list of IPs currently listening on
	sync.RWMutex                       // Mutex for list
	ifacesExist  map[string][]net.Addr // list of currently known interfaces with list of IP addresses
}

// NewIPList creates a new list
func NewIPList() (list *ipList) {
	return &ipList{
		ipListen:    make(map[string]struct{}),
		ifacesExist: make(map[string][]net.Addr),
	}
}

// Add adds a listening IP:Port to the list.
func (list *ipList) Add(addr *net.UDPAddr) {
	list.Lock()
	list.ipListen[net.JoinHostPort(addr.IP.String(), strconv.Itoa(addr.Port))] = struct{}{}
	list.Unlock()
}

// Remove removes a listening address from the list
func (list *ipList) Remove(addr *net.UDPAddr) {
	list.Lock()
	delete(list.ipListen, net.JoinHostPort(addr.IP.String(), strconv.Itoa(addr.Port)))
	list.Unlock()
}

// IsAddressSelf checks if the senders address is actually listening address. This prevents loopback packets from being considered.
// Note: This does not work when listening on 0.0.0.0 or ::1 and binding the sending socket to that.
func (list *ipList) IsAddressSelf(addr *net.UDPAddr) bool {
	if addr == nil {
		return false
	}

	// do not use addr.String() since it addds the Zone for IPv6 which may be ambiguous (can be adapter name or address literal).
	list.RLock()
	_, ok := list.ipListen[net.JoinHostPort(addr.IP.String(), strconv.Itoa(addr.Port))]
	list.RUnlock()
	return ok
}
