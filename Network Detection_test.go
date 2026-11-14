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

// TestIsIPLocal checks the private, link-local and loopback address ranges and their neighbours.
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
		{"100.64.0.1", false},
		{"127.0.0.1", true},
		{"127.255.255.254", true},
		{"126.0.0.1", false},
		{"128.0.0.1", false},
		{"fc00::1", true},
		{"fd12:3456::1", true},
		{"fe80::1", true},
		{"2001:db8::1", false},
		{"::1", true},
		{"::2", false},
	}

	for _, test := range tests {
		if result := IsIPLocal(net.ParseIP(test.ip)); result != test.local {
			t.Errorf("IsIPLocal(%s) = %t, expected %t", test.ip, result, test.local)
		}
	}
}

// TestIsIPCarrierNAT checks the carrier-grade NAT address range and its neighbours.
func TestIsIPCarrierNAT(t *testing.T) {
	tests := []struct {
		ip     string
		result bool
	}{
		{"100.63.255.255", false},
		{"100.64.0.0", true},
		{"100.64.0.1", true},
		{"100.127.255.255", true},
		{"100.128.0.0", false},
		{"::ffff:100.64.0.1", true},
		{"10.0.0.1", false},
		{"8.8.8.8", false},
		{"2001:db8::1", false},
	}

	for _, test := range tests {
		if result := isIPCarrierNAT(net.ParseIP(test.ip)); result != test.result {
			t.Errorf("isIPCarrierNAT(%s) = %t, expected %t", test.ip, result, test.result)
		}
	}

	// The 4-byte form of an IPv4 address, as a connection and a decoded message carry it.
	if !isIPCarrierNAT(net.ParseIP("100.64.0.1").To4()) || isIPCarrierNAT(net.ParseIP("100.128.0.0").To4()) {
		t.Errorf("isIPCarrierNAT: unexpected result for the 4-byte form")
	}
}

// TestIsIPLinkLocalOrLoopback checks the link-local and loopback address ranges and their neighbours.
func TestIsIPLinkLocalOrLoopback(t *testing.T) {
	tests := []struct {
		ip     string
		result bool
	}{
		{"169.254.0.1", true},
		{"169.254.255.255", true},
		{"169.253.0.1", false},
		{"169.255.0.1", false},
		{"127.0.0.1", true},
		{"127.255.255.254", true},
		{"126.0.0.1", false},
		{"128.0.0.1", false},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"8.8.8.8", false},
		{"fe80::1", true},
		{"febf::1", true},
		{"fec0::1", false},
		{"::1", true},
		{"::2", false},
		{"fc00::1", false},
		{"fd12:3456::1", false},
		{"2001:db8::1", false},
	}

	for _, test := range tests {
		if result := isIPLinkLocalOrLoopback(net.ParseIP(test.ip)); result != test.result {
			t.Errorf("isIPLinkLocalOrLoopback(%s) = %t, expected %t", test.ip, result, test.result)
		}
	}
}
