/*
File Name:  Bootstrap_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"

	"github.com/PeernetOfficial/core/protocol"
)

// testRecordAddresses returns the addresses of a peer record for the IPs. The internal port is set, the external port is not.
func testRecordAddresses(port uint16, ips ...string) (addresses []protocol.PeerRecordAddress) {
	for _, ip := range ips {
		addresses = append(addresses, protocol.PeerRecordAddress{IP: net.ParseIP(ip), Port: port, PortReportedInternal: port})
	}
	return addresses
}

// TestPeerRecordToAddresses checks that a local address of a reported peer is only returned if the record was received via a local connection,
// and that a link-local or loopback address is not returned.
func TestPeerRecordToAddresses(t *testing.T) {
	tests := []struct {
		ipv4, ipv6 []string
		allowLocal bool
		expected   []string
	}{
		{[]string{"203.0.113.7"}, []string{"2001:db8::7"}, false, []string{"203.0.113.7", "2001:db8::7"}},
		{[]string{"203.0.113.7"}, []string{"2001:db8::7"}, true, []string{"203.0.113.7", "2001:db8::7"}},
		{[]string{"192.168.1.7"}, []string{"fd00::7"}, false, nil},
		{[]string{"192.168.1.7"}, []string{"fd00::7"}, true, []string{"192.168.1.7", "fd00::7"}},
		{[]string{"10.0.0.7"}, []string{"2001:db8::7"}, false, []string{"2001:db8::7"}},
		{nil, nil, true, nil},

		// all addresses of the record are returned
		{[]string{"203.0.113.7", "203.0.113.8", "203.0.113.9"}, []string{"2001:db8::7", "2001:db8::8", "2001:db8::9"}, false, []string{"203.0.113.7", "203.0.113.8", "203.0.113.9", "2001:db8::7", "2001:db8::8", "2001:db8::9"}},
		{[]string{"203.0.113.7", "192.168.1.7", "203.0.113.9"}, []string{"fd00::7", "2001:db8::8"}, false, []string{"203.0.113.7", "203.0.113.9", "2001:db8::8"}},
		{[]string{"203.0.113.7", "192.168.1.7", "203.0.113.9"}, []string{"fd00::7", "2001:db8::8"}, true, []string{"203.0.113.7", "192.168.1.7", "203.0.113.9", "fd00::7", "2001:db8::8"}},
		{nil, []string{"2001:db8::7", "2001:db8::8"}, false, []string{"2001:db8::7", "2001:db8::8"}},

		// link-local and loopback addresses
		{[]string{"203.0.113.7"}, []string{"fe80::7"}, false, []string{"203.0.113.7"}},
		{[]string{"203.0.113.7"}, []string{"fe80::7"}, true, []string{"203.0.113.7"}},
		{[]string{"169.254.1.7"}, nil, false, nil},
		{[]string{"169.254.1.7"}, nil, true, nil},
		{[]string{"127.0.0.1"}, []string{"::1"}, false, nil},
		{[]string{"127.0.0.1"}, []string{"::1"}, true, nil},
		{[]string{"127.0.0.1", "192.168.1.7", "169.254.1.7"}, []string{"::1", "fe80::7", "fd00::7"}, true, []string{"192.168.1.7", "fd00::7"}},
		{[]string{"127.0.0.1", "192.168.1.7", "169.254.1.7"}, []string{"::1", "fe80::7", "fd00::7"}, false, nil},

		// carrier-grade NAT addresses
		{[]string{"100.64.0.1"}, nil, false, nil},
		{[]string{"100.64.0.1"}, nil, true, nil},
		{[]string{"100.64.0.1", "203.0.113.7"}, []string{"2001:db8::7"}, false, []string{"203.0.113.7", "2001:db8::7"}},
		{[]string{"100.127.255.255", "203.0.113.7", "10.0.0.7"}, nil, true, []string{"203.0.113.7", "10.0.0.7"}},

		// unspecified addresses
		{[]string{"0.0.0.0", "203.0.113.7"}, []string{"::"}, true, []string{"203.0.113.7"}},
	}

	for _, test := range tests {
		record := &protocol.PeerRecord{IPv4: testRecordAddresses(1001, test.ipv4...), IPv6: testRecordAddresses(1002, test.ipv6...)}

		addresses := peerRecordToAddresses(record, test.allowLocal)

		if len(addresses) != len(test.expected) {
			t.Errorf("record %s / %s allow local %t: %d addresses, expected %d", test.ipv4, test.ipv6, test.allowLocal, len(addresses), len(test.expected))
			continue
		}
		for n, address := range addresses {
			if !address.IP.Equal(net.ParseIP(test.expected[n])) || address.PortInternal == 0 || address.Port != address.PortInternal {
				t.Errorf("record %s / %s allow local %t: address %s port %d internal %d, expected %s", test.ipv4, test.ipv6, test.allowLocal, address.IP.String(), address.Port, address.PortInternal, test.expected[n])
			}
		}
	}

	// The external port is used if reported.
	record := &protocol.PeerRecord{IPv4: []protocol.PeerRecordAddress{{IP: net.ParseIP("203.0.113.7"), Port: 1001, PortReportedInternal: 112, PortReportedExternal: 2001}, {IP: net.ParseIP("203.0.113.8"), Port: 1002, PortReportedInternal: 112}}}
	if addresses := peerRecordToAddresses(record, false); len(addresses) != 2 || addresses[0].Port != 2001 || addresses[0].PortInternal != 112 || addresses[1].Port != 1002 || addresses[1].PortInternal != 112 {
		t.Errorf("external port: unexpected addresses %v", addresses)
	}
}

// TestIsReturnedPeerBadQuality checks which peer records reported by another peer are discarded.
func TestIsReturnedPeerBadQuality(t *testing.T) {
	self, _, _ := ed25519.GenerateKey(rand.Reader)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	backend := &Backend{peerPublicKey: self}

	noInternalPort := protocol.PeerRecordAddress{IP: net.ParseIP("203.0.113.9"), Port: 1001}

	tests := []struct {
		record protocol.PeerRecord
		bad    bool
	}{
		{protocol.PeerRecord{PublicKey: other, IPv4: testRecordAddresses(1001, "203.0.113.7")}, false},
		{protocol.PeerRecord{PublicKey: other, IPv6: testRecordAddresses(1001, "2001:db8::7")}, false},
		{protocol.PeerRecord{PublicKey: other, IPv4: testRecordAddresses(1001, "203.0.113.7", "203.0.113.8", "203.0.113.9"), IPv6: testRecordAddresses(1001, "2001:db8::7", "2001:db8::8", "2001:db8::9")}, false},
		{protocol.PeerRecord{PublicKey: other}, true}, // no address
		{protocol.PeerRecord{PublicKey: self, IPv4: testRecordAddresses(1001, "203.0.113.7")}, true},
		{protocol.PeerRecord{PublicKey: other, IPv4: []protocol.PeerRecordAddress{noInternalPort}}, true},
		{protocol.PeerRecord{PublicKey: other, IPv6: []protocol.PeerRecordAddress{noInternalPort}}, true},
		{protocol.PeerRecord{PublicKey: other, IPv4: append(testRecordAddresses(1001, "203.0.113.7", "203.0.113.8"), noInternalPort)}, true},
		{protocol.PeerRecord{PublicKey: other, IPv4: testRecordAddresses(1001, "203.0.113.7"), IPv6: []protocol.PeerRecordAddress{testRecordAddresses(1001, "2001:db8::7")[0], noInternalPort}}, true},
	}

	for n, test := range tests {
		if bad := backend.isReturnedPeerBadQuality(&test.record); bad != test.bad {
			t.Errorf("record %d: bad quality %t, expected %t", n, bad, test.bad)
		}
	}
}
