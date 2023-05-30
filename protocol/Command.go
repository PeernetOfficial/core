/*
File Username:  Command.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package protocol

// Commands between peers
const (
	// Peer List Management
	CommandAnnouncement   = 0 // Announcement
	CommandResponse       = 1 // Response
	CommandPing           = 2 // Keep-alive message (no payload).
	CommandPong           = 3 // Response to ping (no payload).
	CommandLocalDiscovery = 4 // Local discovery
	CommandTraverse       = 5 // Help establish a connection between 2 remote peers

	// Blockchain
	CommandGetBlock = 6 // Request blocks for specified peer.

	// File Discovery
	CommandTransfer = 8 // File transfer.

	// Debug
	CommandChat = 10 // Chat message [debug]
)
