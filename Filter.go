/*
File Name:  Filter.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Filters allow the caller to intercept events to log, modify, or prevent.
*/

package core

import (
	"log"

	"github.com/PeernetOfficial/core/dht"
)

// Filters contains all functions to install the hook. Use nil for unused.
// The functions are called sequentially and block execution; if the filter takes a long time it should start a Go routine.
var Filters struct {
	// NewPeer is called every time a new peer, that is one that is not currently in the peer list.
	// Note that peers might be removed from peer lists and reappear quickly, i.e. this function may be called multiple times for the same peers.
	// The filter must maintain its own map of unique peer IDs if actual uniqueness of new peers is desired.
	NewPeer func(peer *PeerInfo, connection *Connection)

	// NewPeerConnection is called for each new established connection to a peer. Note that connections might be dropped and reconnected at anytime.
	NewPeerConnection func(peer *PeerInfo, connection *Connection)

	// LogError is called for any error. If this function is overwritten by the caller, the caller must write errors into the log file if desired, or call DefaultLogError.
	LogError func(function, format string, v ...interface{})

	// DHTSearchStatus is called with updates of searches in the DHT. It allows to see the live progress of searches.
	DHTSearchStatus func(client *dht.SearchClient, function, format string, v ...interface{})

	// IncomingRequest receives all incoming information requests. The action field is set accordingly.
	IncomingRequest func(peer *PeerInfo, Action int, Key []byte, Info interface{})
}

func initFilters() {
	// Set default filters to blank functions so they can be safely called without constant nil checks.
	// Only if not already set before init.

	if Filters.NewPeer == nil {
		Filters.NewPeer = func(peer *PeerInfo, connection *Connection) {}
	}
	if Filters.NewPeerConnection == nil {
		Filters.NewPeerConnection = func(peer *PeerInfo, connection *Connection) {}
	}
	if Filters.DHTSearchStatus == nil {
		Filters.DHTSearchStatus = func(client *dht.SearchClient, function, format string, v ...interface{}) {}
	}
	if Filters.LogError == nil {
		Filters.LogError = DefaultLogError
	}
	if Filters.IncomingRequest == nil {
		Filters.IncomingRequest = func(peer *PeerInfo, Action int, Key []byte, Info interface{}) {}
	}
}

// DefaultLogError is the default error logging function
func DefaultLogError(function, format string, v ...interface{}) {
	log.Printf("["+function+"] "+format, v...)
}
