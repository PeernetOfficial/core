/*
File Name:  Store.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"encoding/hex"
	"fmt"

	"github.com/PeernetOfficial/core/store"
)

// Warehouse contains all key-value data served via DHT
var Warehouse store.Store

// TODO: Via descriptors, files stored by other peers

func initStore() {
	Warehouse = store.NewMemoryStore()
}

// announcementGetData returns data for an announcement
func announcementGetData(hash []byte) (stored bool, data []byte) {
	// TODO: Create RetrieveIfSize to prevent files larger than EmbeddedFileSizeMax from being loaded
	data, found := Warehouse.Retrieve(hash)
	if !found {
		return false, nil
	}

	if len(data) <= EmbeddedFileSizeMax {
		return true, data
	}

	return true, nil
}

// announcementStore handles an incoming announcement by another peer about storing data
func (peer *PeerInfo) announcementStore(records []InfoStore) {
	// TODO: Only store the other peers data if certain conditions are met:
	// - enough storage available
	// - not exceeding record count per peer
	// - not exceeding total record count limit
	// - not exceeding record count per CIDR
	for _, record := range records {
		fmt.Printf("Remote node %s stores hash %s (size %d type %d)\n", hex.EncodeToString(peer.NodeID), hex.EncodeToString(record.ID.Hash), record.Size, record.Type)
	}
}
