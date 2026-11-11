/*
File Name:  Bootstrap_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"net"
	"testing"

	"github.com/PeernetOfficial/core/protocol"
)

// TestPeerRecordToAddresses checks that a local address of a reported peer is only returned if the record was received via a local connection.
func TestPeerRecordToAddresses(t *testing.T) {
	tests := []struct {
		ipv4, ipv6 string
		allowLocal bool
		expected   []string
	}{
		{"203.0.113.7", "2001:db8::7", false, []string{"203.0.113.7", "2001:db8::7"}},
		{"203.0.113.7", "2001:db8::7", true, []string{"203.0.113.7", "2001:db8::7"}},
		{"192.168.1.7", "fd00::7", false, nil},
		{"192.168.1.7", "fd00::7", true, []string{"192.168.1.7", "fd00::7"}},
		{"10.0.0.7", "2001:db8::7", false, []string{"2001:db8::7"}},
		{"203.0.113.7", "fe80::7", false, []string{"203.0.113.7"}},
		{"169.254.1.7", "", false, nil},
		{"169.254.1.7", "", true, []string{"169.254.1.7"}},
	}

	for _, test := range tests {
		record := &protocol.PeerRecord{IPv4: net.ParseIP(test.ipv4), IPv4Port: 1001, IPv4PortReportedInternal: 1001, IPv6: net.ParseIP(test.ipv6), IPv6Port: 1002, IPv6PortReportedInternal: 1002}

		addresses := peerRecordToAddresses(record, test.allowLocal)

		if len(addresses) != len(test.expected) {
			t.Errorf("record %s / %s allow local %t: %d addresses, expected %d", test.ipv4, test.ipv6, test.allowLocal, len(addresses), len(test.expected))
			continue
		}
		for n, address := range addresses {
			if !address.IP.Equal(net.ParseIP(test.expected[n])) {
				t.Errorf("record %s / %s allow local %t: address %s, expected %s", test.ipv4, test.ipv6, test.allowLocal, address.IP.String(), test.expected[n])
			}
		}
	}
}
