/*
File Name:  Networks.go
Copyright:  2021 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package core

import (
	"sync"

	"github.com/PeernetOfficial/core/protocol"
)

// Networks is the collection of all connected networks
type Networks struct {
	// networks is a list of all connected networks
	networks4, networks6 []*Network

	// Mutex for both network lists. Higher granularity currently not needed.
	sync.RWMutex

	// countListenX is the number of networks listened to, excluding link-local only listeners. This number might be different than len(networksN).
	// This is useful to determine if there are any IPv4 or IPv6 listeners for potential external connections. This can be used to determine IPv4_LISTEN and IPv6_LISTEN.
	countListen4, countListen6 int64

	// channel for processing incoming decoded packets by workers, across all networks
	rawPacketsIncoming  chan networkWire
	litePacketsIncoming chan networkWire

	// Sequences keeps track of all message sequence number, regardless of the network connection.
	Sequences *protocol.SequenceManager

	// Keep track of valid IDs for lite packets.
	LiteRouter *protocol.LiteRouter

	// ipListen keeps a simple list of IPs listened to. This allows quickly identifying if an IP matches with a listened one.
	ipListen *ipList

	// UPnP data
	upnpListInterfaces map[string]struct{}
	upnpMutex          sync.RWMutex

	// backend
	backend *Backend
}

//  ReplyTimeout is the round-trip timeout for message sequences.
const ReplyTimeout = 20

func (backend *Backend) initMessageSequence() {
	backend.networks = &Networks{backend: backend}

	backend.networks.rawPacketsIncoming = make(chan networkWire, 1000)  // buffer up to 1000 UDP packets before they get buffered by the OS network stack and eventually dropped
	backend.networks.litePacketsIncoming = make(chan networkWire, 1000) // buffer up to 1000 UDP packets before they get buffered by the OS network stack and eventually dropped

	backend.networks.Sequences = protocol.NewSequenceManager(ReplyTimeout)
	backend.networks.LiteRouter = protocol.NewLiteRouter()

	backend.networks.ipListen = NewIPList()
}

// List of all lite sessions
func (backend *Backend) LiteSessions() (sessions []*protocol.LiteID) {
	return backend.networks.LiteRouter.All()
}
