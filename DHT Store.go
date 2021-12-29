/*
File Name:  DHT Store.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"github.com/PeernetOfficial/core/protocol"
	"github.com/PeernetOfficial/core/store"
)

// TODO: Via descriptors, files stored by other peers

func (backend *Backend) initStore() {
	backend.dhtStore = store.NewMemoryStore()
}

// announcementGetData returns data for an announcement
func (peer *PeerInfo) announcementGetData(hash []byte) (stored bool, data []byte) {
	// TODO: Create RetrieveIfSize to prevent files larger than EmbeddedFileSizeMax from being loaded
	data, found := peer.Backend.dhtStore.Get(hash)
	if !found {
		return false, nil
	}

	if len(data) <= protocol.EmbeddedFileSizeMax {
		return true, data
	}

	return true, nil
}

// announcementStore handles an incoming announcement by another peer about storing data
func (peer *PeerInfo) announcementStore(records []protocol.InfoStore) {
	// TODO: Only store the other peers data if certain conditions are met:
	// - enough storage available
	// - not exceeding record count per peer
	// - not exceeding total record count limit
	// - not exceeding record count per CIDR
	//for _, record := range records {
	//fmt.Printf("Remote node %s stores hash %s (size %d type %d)\n", hex.EncodeToString(peer.NodeID), hex.EncodeToString(record.ID.Hash), record.Size, record.Type)
	//Warehouse.Store(record.ID.Hash, )
	// TODO: Request data from remote node.
	//peer.sendAnnouncement(false, false, nil, []KeyHash{record.ID}, nil, nil)
	//}
}
