/*
File Name:  Connection_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"net"
	"testing"
)

// TestGetConnection2Share checks which connection of a peer is shared: a local one only if allowed, and then preferred. A link-local or loopback one is not shared.
func TestGetConnection2Share(t *testing.T) {
	newConnection := func(ip string) *Connection {
		return &Connection{Address: &net.UDPAddr{IP: net.ParseIP(ip), Port: 112}, PortInternal: 112}
	}

	public4 := newConnection("203.0.113.7")
	local4 := newConnection("192.168.1.7")
	public6 := newConnection("2001:db8::7")
	local6 := newConnection("fd00::7")

	// The public IPv4 connection is the latest one.
	peer := &PeerInfo{connectionActive: []*Connection{public4, local4, public6, local6}, connectionLatest: public4}

	tests := []struct {
		allowLocal, allowIPv4, allowIPv6 bool
		expected                         *Connection
	}{
		{false, true, false, public4},
		{true, true, false, local4},
		{false, false, true, public6},
		{true, false, true, local6},
		{false, false, false, nil},
	}

	for _, test := range tests {
		if result := peer.GetConnection2Share(test.allowLocal, test.allowIPv4, test.allowIPv6); result != test.expected {
			t.Errorf("allow local %t IPv4 %t IPv6 %t: unexpected connection %v", test.allowLocal, test.allowIPv4, test.allowIPv6, result)
		}
	}

	// A peer with public connections only: those are shared also if local ones are allowed.
	peer = &PeerInfo{connectionActive: []*Connection{public4, public6}, connectionLatest: public6}

	if result := peer.GetConnection2Share(true, true, false); result != public4 {
		t.Errorf("public only, IPv4: unexpected connection %v", result)
	}
	if result := peer.GetConnection2Share(true, false, true); result != public6 {
		t.Errorf("public only, IPv6: unexpected connection %v", result)
	}

	// A peer with local connections only: nothing is shared if local ones are not allowed.
	peer = &PeerInfo{connectionActive: []*Connection{local4, local6}, connectionLatest: local4}

	if result := peer.GetConnection2Share(false, true, true); result != nil {
		t.Errorf("local only, local not allowed: unexpected connection %v", result)
	}
	if result := peer.GetConnection2Share(true, true, false); result != local4 {
		t.Errorf("local only, IPv4: unexpected connection %v", result)
	}

	// A peer known only by link-local and loopback addresses is not shared, over a local connection too.
	peer = &PeerInfo{connectionActive: []*Connection{newConnection("169.254.1.7"), newConnection("127.0.0.1"), newConnection("fe80::7"), newConnection("::1")}}
	peer.connectionLatest = peer.connectionActive[0]

	for _, allowLocal := range []bool{false, true} {
		if result := peer.GetConnection2Share(allowLocal, true, true); result != nil {
			t.Errorf("link-local and loopback only, allow local %t: unexpected connection %v", allowLocal, result)
		}
		if peer.IsConnectable(allowLocal, true, true) {
			t.Errorf("link-local and loopback only, allow local %t: peer is connectable", allowLocal)
		}
		if record := peer.peer2Record(allowLocal, true, true, 3); record != nil {
			t.Errorf("link-local and loopback only, allow local %t: unexpected peer record %v", allowLocal, record)
		}
	}

	// Link-local and loopback connections are skipped, also as the latest connection.
	linkLocal4 := newConnection("169.254.1.7")
	peer = &PeerInfo{connectionActive: []*Connection{linkLocal4, newConnection("::1"), local4, public4, local6}, connectionLatest: linkLocal4}

	if result := peer.GetConnection2Share(true, true, false); result != local4 {
		t.Errorf("link-local latest, allow local: unexpected connection %v", result)
	}
	if result := peer.GetConnection2Share(false, true, false); result != public4 {
		t.Errorf("link-local latest: unexpected connection %v", result)
	}
	if result := peer.GetConnection2Share(true, false, true); result != local6 {
		t.Errorf("loopback IPv6, allow local: unexpected connection %v", result)
	}
}

// TestGetConnections2Share checks the order and the count of the connections shared.
func TestGetConnections2Share(t *testing.T) {
	newConnection := func(ip string) *Connection {
		return &Connection{Address: &net.UDPAddr{IP: net.ParseIP(ip), Port: 112}, PortInternal: 112}
	}

	public4a := newConnection("203.0.113.7")
	public4b := newConnection("203.0.113.8")
	public4c := newConnection("203.0.113.9")
	local4a := newConnection("192.168.1.7")
	local4b := newConnection("10.0.0.7")
	public6 := newConnection("2001:db8::7")
	sameAddress := newConnection("203.0.113.8") // the address of public4b, connected via another network
	noInternalPort := &Connection{Address: &net.UDPAddr{IP: net.ParseIP("203.0.113.10"), Port: 112}}

	// public4b is the latest connection.
	peer := &PeerInfo{connectionActive: []*Connection{newConnection("127.0.0.1"), noInternalPort, public4a, local4a, public4b, sameAddress, public6, local4b, public4c, newConnection("169.254.1.7")}, connectionLatest: public4b}

	tests := []struct {
		allowLocal, allowIPv4, allowIPv6 bool
		limit                            int
		expected                         []*Connection
	}{
		{false, true, false, 3, []*Connection{public4b, public4a, public4c}},
		{false, true, false, 2, []*Connection{public4b, public4a}},
		{false, true, false, 1, []*Connection{public4b}},
		{true, true, false, 3, []*Connection{local4a, local4b, public4b}},
		{true, true, false, 1, []*Connection{local4a}},
		{true, true, false, 10, []*Connection{local4a, local4b, public4b, public4a, public4c}},
		{false, false, true, 3, []*Connection{public6}},
		{true, false, true, 3, []*Connection{public6}},
		{false, true, true, 10, []*Connection{public4b, public4a, public6, public4c}},
		{false, false, false, 3, nil},
		{false, true, false, 0, nil},
	}

	for _, test := range tests {
		result := peer.GetConnections2Share(test.allowLocal, test.allowIPv4, test.allowIPv6, test.limit)

		if len(result) != len(test.expected) {
			t.Errorf("allow local %t IPv4 %t IPv6 %t limit %d: %d connections, expected %d", test.allowLocal, test.allowIPv4, test.allowIPv6, test.limit, len(result), len(test.expected))
			continue
		}
		for n := range result {
			if result[n] != test.expected[n] {
				t.Errorf("allow local %t IPv4 %t IPv6 %t limit %d: connection %d is %s, expected %s", test.allowLocal, test.allowIPv4, test.allowIPv6, test.limit, n, result[n].Address.IP.String(), test.expected[n].Address.IP.String())
			}
		}
	}
}
