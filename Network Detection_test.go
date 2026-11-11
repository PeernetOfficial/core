/*
File Name:  Network Detection_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"net"
	"testing"
)

// TestIsIPLocal checks the private and link-local address ranges and their neighbours.
func TestIsIPLocal(t *testing.T) {
	tests := []struct {
		ip    string
		local bool
	}{
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"172.32.0.1", false},
		{"192.168.1.1", true},
		{"192.169.1.1", false},
		{"169.254.0.1", true},
		{"169.254.255.255", true},
		{"169.253.0.1", false},
		{"169.255.0.1", false},
		{"8.8.8.8", false},
		{"fc00::1", true},
		{"fd12:3456::1", true},
		{"fe80::1", true},
		{"2001:db8::1", false},
	}

	for _, test := range tests {
		if result := IsIPLocal(net.ParseIP(test.ip)); result != test.local {
			t.Errorf("IsIPLocal(%s) = %t, expected %t", test.ip, result, test.local)
		}
	}
}
