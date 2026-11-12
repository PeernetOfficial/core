/*
File Name:  Command Traverse_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"net"
	"testing"

	"github.com/PeernetOfficial/core/protocol"
)

// TestTraverseAddresses checks which addresses of the original sender in a received traverse message are used:
// a local address only if the message was received via a local connection, a link-local or loopback address never.
func TestTraverseAddresses(t *testing.T) {
	tests := []struct {
		ipv4, ipv6 string
		allowLocal bool
		expected   []string
	}{
		{"203.0.113.7", "2001:db8::7", false, []string{"203.0.113.7", "2001:db8::7"}},
		{"203.0.113.7", "2001:db8::7", true, []string{"203.0.113.7", "2001:db8::7"}},
		{"203.0.113.7", "::", false, []string{"203.0.113.7"}},
		{"0.0.0.0", "2001:db8::7", false, []string{"2001:db8::7"}},
		{"0.0.0.0", "::", true, nil},
		{"192.168.1.7", "fd00::7", false, nil},
		{"192.168.1.7", "fd00::7", true, []string{"192.168.1.7", "fd00::7"}},
		{"10.0.0.7", "2001:db8::7", false, []string{"2001:db8::7"}},

		// link-local and loopback addresses
		{"169.254.1.7", "fe80::7", false, nil},
		{"169.254.1.7", "fe80::7", true, nil},
		{"127.0.0.1", "::1", false, nil},
		{"127.0.0.1", "::1", true, nil},
		{"127.0.0.1", "2001:db8::7", true, []string{"2001:db8::7"}},
		{"192.168.1.7", "fe80::7", true, []string{"192.168.1.7"}},
		{"169.254.1.7", "fd00::7", true, []string{"fd00::7"}},
	}

	for _, test := range tests {
		msg := &protocol.MessageTraverse{IPv4: net.ParseIP(test.ipv4).To4(), PortIPv4: 1001, IPv6: net.ParseIP(test.ipv6), PortIPv6: 1002}

		addresses := traverseAddresses(msg, test.allowLocal)

		if len(addresses) != len(test.expected) {
			t.Errorf("traverse %s / %s allow local %t: %d addresses, expected %d", test.ipv4, test.ipv6, test.allowLocal, len(addresses), len(test.expected))
			continue
		}
		for n, address := range addresses {
			if !address.IP.Equal(net.ParseIP(test.expected[n])) {
				t.Errorf("traverse %s / %s allow local %t: address %s, expected %s", test.ipv4, test.ipv6, test.allowLocal, address.IP.String(), test.expected[n])
			}
		}
	}

	// The external port is used if reported.
	msg := &protocol.MessageTraverse{IPv4: net.ParseIP("203.0.113.7").To4(), PortIPv4: 1001, PortIPv4ReportedExternal: 2001, IPv6: net.ParseIP("2001:db8::7"), PortIPv6: 1002}
	if addresses := traverseAddresses(msg, false); len(addresses) != 2 || addresses[0].Port != 2001 || addresses[1].Port != 1002 {
		t.Errorf("external port: unexpected addresses %v", addresses)
	}
}
