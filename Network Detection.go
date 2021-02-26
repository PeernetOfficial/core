/*
File Name:  Network Detection.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package core

import "net"

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
