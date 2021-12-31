/*
File Name:  Networks.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"os"
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
	rawPacketsIncoming chan networkWire

	// Sequences keeps track of all message sequence number, regardless of the network connection.
	Sequences *protocol.SequenceManager

	// ipListen keeps a simple list of IPs listened to. This allows quickly identifying if an IP matches with a listened one.
	ipListen *ipList

	// localFirewall indicates if a local firewall may drop unsolicited incoming packets
	localFirewall bool

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

	backend.networks.rawPacketsIncoming = make(chan networkWire, 1000) // buffer up to 1000 UDP packets before they get buffered by the OS network stack and eventually dropped

	backend.networks.Sequences = protocol.NewSequenceManager(ReplyTimeout)

	backend.networks.ipListen = NewIPList()

	// There is currently no suitable live firewall detection code. Instead, there is the config flag.
	// Windows: If the user runs as non-admin, it can be assumed that the Windows Firewall creates a rule to drop unsolicited incoming packets.
	// Changing the Windows Firewall (via netsh or otherwise) requires elevated admin rights.
	// This flag will be passed on to other peers to indicate that uncontacted peers shall use the Traverse message for establishing connections.
	backend.firewallDetectIndicatorFile()
	backend.networks.localFirewall = backend.Config.LocalFirewall
}

const firewallIndicatorFile = "firewallnotset"

// Detects the file "firewallnotset". It may be set by an Peernet client installer to indicate the presence of a local firewall.
func (backend *Backend) firewallDetectIndicatorFile() {
	if _, err := os.Stat(firewallIndicatorFile); err == nil {
		// If the config firewall flag isn't set, set it.
		if !backend.Config.LocalFirewall {
			backend.Config.LocalFirewall = true

			backend.SaveConfig()
		}

		// the indicator is one-time only, remove
		os.Remove(firewallIndicatorFile)
	}
}
