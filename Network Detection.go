/*
File Username:  Network Detection.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"net"
	"strings"
	"time"
)

// FindInterfaceByIP finds an interface based on the IP. The IP must be available at the interface.
func FindInterfaceByIP(ip net.IP) (iface *net.Interface, ipnet *net.IPNet) {
	interfaceList, err := net.Interfaces()
	if err != nil {
		return nil, nil
	}

	// iterate through all interfaces
	for _, ifaceSingle := range interfaceList {
		addresses, err := ifaceSingle.Addrs()
		if err != nil {
			continue
		}

		// iterate through all IPs of the interfaces
		for _, address := range addresses {
			addressIP := address.(*net.IPNet).IP

			if addressIP.Equal(ip) {
				return &ifaceSingle, address.(*net.IPNet)
			}
		}
	}

	return nil, nil
}

// NetworkListIPs returns a list of all IPs
func NetworkListIPs() (IPs []net.IP, err error) {

	interfaceList, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	// iterate through all interfaces
	for _, ifaceSingle := range interfaceList {
		addresses, err := ifaceSingle.Addrs()
		if err != nil {
			continue
		}

		// iterate through all IPs of the interfaces
		for _, address := range addresses {
			addressIP := address.(*net.IPNet).IP
			IPs = append(IPs, addressIP)
		}
	}

	return IPs, nil
}

// IsIPv4 checks if an IP address is IPv4
func IsIPv4(IP net.IP) bool {
	return IP.To4() != nil
}

// IsIPv6 checks if an IP address is IPv6
func IsIPv6(IP net.IP) bool {
	return IP.To4() == nil && IP.To16() != nil
}

// IsNetworkErrorFatal checks if a network error indicates a broken connection.
// Not every network error indicates a broken connection. This function prevents from over-dropping connections.
func IsNetworkErrorFatal(err error) bool {
	if err == nil {
		return false
	}

	// Windows: A common error when the network adapter is disabled is "wsasendto: The requested address is not valid in its context".
	if strings.Contains(err.Error(), "requested address is not valid in its context") {
		return true
	}

	return false
}

// changeMonitorFrequency is the frequency in seconds to check for a network change
const changeMonitorFrequency = 10

// networkChangeMonitor() monitors for network changes to act accordingly
func (nets *Networks) networkChangeMonitor() {
	// If manual IPs are entered, no need for monitoring for any network changes.
	if len(nets.backend.Config.Listen) > 0 {
		return
	}

	for {
		time.Sleep(time.Second * changeMonitorFrequency)

		interfaceList, err := net.Interfaces()
		if err != nil {
			nets.backend.LogError("networkChangeMonitor", "enumerating network adapters failed: %s\n", err.Error())
			continue
		}

		ifacesNew := make(map[string][]net.Addr)

		for _, iface := range interfaceList {
			addressesNew, err := iface.Addrs()
			if err != nil {
				nets.backend.LogError("networkChangeMonitor", "enumerating IPs for network adapter '%s': %s\n", iface.Name, err.Error())
				continue
			}
			ifacesNew[iface.Name] = addressesNew

			// was the interface added?
			addressesExist, ok := nets.ipListen.ifacesExist[iface.Name]
			if !ok {
				nets.networkChangeInterfaceNew(iface, addressesNew)
			} else {
				// new IPs added for this interface?
				for _, addr := range addressesNew {
					exists := false
					for _, exist := range addressesExist {
						if exist.String() == addr.String() {
							exists = true
							break
						}
					}

					if !exists {
						nets.networkChangeIPNew(iface, addr)
					}
				}

				// were IPs removed from this interface
				for _, exist := range addressesExist {
					removed := true
					for _, addr := range addressesNew {
						if exist.String() == addr.String() {
							removed = false
							break
						}
					}

					if removed {
						nets.networkChangeIPRemove(iface, exist)
					}
				}
			}
		}

		// was an existing interface removed?
		for ifaceExist, addressesExist := range nets.ipListen.ifacesExist {
			if _, ok := ifacesNew[ifaceExist]; !ok {
				nets.networkChangeInterfaceRemove(ifaceExist, addressesExist)
			}
		}

		nets.ipListen.ifacesExist = ifacesNew
	}
}

// networkChangeInterfaceNew is called when a new interface is detected
func (nets *Networks) networkChangeInterfaceNew(iface net.Interface, addresses []net.Addr) {
	nets.backend.LogError("networkChangeInterfaceNew", "new interface '%s' (%d IPs)\n", iface.Name, len(addresses))

	networksNew := nets.InterfaceStart(iface, addresses)

	for _, network := range networksNew {
		go network.upnpAuto()
	}

	go nets.backend.nodesDHT.RefreshBuckets(0)
}

// networkChangeInterfaceRemove is called when an existing interface is removed
func (nets *Networks) networkChangeInterfaceRemove(iface string, addresses []net.Addr) {
	nets.RLock()
	defer nets.RUnlock()

	nets.backend.LogError("networkChangeInterfaceRemove", "removing interface '%s' (%d IPs)\n", iface, len(addresses))

	for n, network := range nets.networks6 {
		if network.iface != nil && network.iface.Name == iface {
			network.Terminate()

			// remove from list
			networksNew := nets.networks6[:n]
			if n < len(nets.networks6)-1 {
				networksNew = append(networksNew, nets.networks6[n+1:]...)
			}
			nets.networks6 = networksNew
		}
	}

	for n, network := range nets.networks4 {
		if network.iface != nil && network.iface.Name == iface {
			network.Terminate()

			// remove from list
			networksNew := nets.networks4[:n]
			if n < len(nets.networks4)-1 {
				networksNew = append(networksNew, nets.networks4[n+1:]...)
			}
			nets.networks4 = networksNew
		}
	}
}

// networkChangeIPNew is called when an existing interface lists a new IP
func (nets *Networks) networkChangeIPNew(iface net.Interface, address net.Addr) {
	nets.backend.LogError("networkChangeIPNew", "new interface '%s' IP %s\n", iface.Name, address.String())

	networksNew := nets.InterfaceStart(iface, []net.Addr{address})

	for _, network := range networksNew {
		go network.upnpAuto()
	}

	go nets.backend.nodesDHT.RefreshBuckets(0)
}

// networkChangeIPRemove is called when an existing interface removes an IP
func (nets *Networks) networkChangeIPRemove(iface net.Interface, address net.Addr) {
	nets.RLock()
	defer nets.RUnlock()

	nets.backend.LogError("networkChangeIPRemove", "remove interface '%s' IP %s\n", iface.Name, address.String())

	for n, network := range nets.networks6 {
		if network.address.IP.Equal(address.(*net.IPNet).IP) {
			network.Terminate()

			// remove from list
			networksNew := nets.networks6[:n]
			if n < len(nets.networks6)-1 {
				networksNew = append(networksNew, nets.networks6[n+1:]...)
			}
			nets.networks6 = networksNew
		}
	}

	for n, network := range nets.networks4 {
		if network.address.IP.Equal(address.(*net.IPNet).IP) {
			network.Terminate()

			// remove from list
			networksNew := nets.networks4[:n]
			if n < len(nets.networks4)-1 {
				networksNew = append(networksNew, nets.networks4[n+1:]...)
			}
			nets.networks4 = networksNew
		}
	}
}

// IsIPLocal reports whether ip is a private (local) address.
// The identification of private, or local, unicast addresses uses address type
// indentification as defined in RFC 1918 for ip4 and RFC 4193 (fc00::/7) for ip6 with the exception of ip4 directed broadcast addresses.
// Unassigned, reserved, multicast and limited-broadcast addresses are not handled and will return false.
// IPv6 link-local addresses (fe80::/10) are included in this check.
func IsIPLocal(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		return ip4[0] == 10 || (ip4[0] == 172 && ip4[1]&0xf0 == 16) || (ip4[0] == 192 && ip4[1] == 168)
	}
	return len(ip) == net.IPv6len &&
		(ip[0]&0xfe == 0xfc || // fc00::/7
			(ip[0] == 0xfe && ip[1]&0xC0 == 0x80)) // fe80::/10
}
