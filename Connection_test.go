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

// TestGetConnection2Share checks which connection of a peer is shared: a local one only if allowed, and then preferred.
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
}
