/*
File Name:  Command.go
Copyright:  2021 Kleissner Ventures Ltd
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
	CommandTraverse       = 5 // Help establish the initial connection between 2 remote peers
	CommandRelay          = 6 // Relay Announcement and Response messages between 2 peers that cannot establish a direct connection

	// Blockchain
	CommandGetBlock = 7 // Request blocks for specified peer.

	// File Discovery
	CommandTransfer = 8 // File transfer.

	// Debug
	CommandChat = 10 // Chat message [debug]
)
