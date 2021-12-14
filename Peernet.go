/*
File Name:  Peernet.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"github.com/PeernetOfficial/core/search"
)

var userAgent = "Peernet Core/0.1" // must be overwritten by the caller

// Init initializes the client. The config must be loaded first!
// The User Agent must be provided in the form "Application Name/1.0".
func Init(UserAgent string) (backend *Backend) {
	if userAgent = UserAgent; userAgent == "" {
		return
	}

	backend = &Backend{}
	currentBackend = backend

	initFilters()
	initPeerID()
	initUserBlockchain()
	initUserWarehouse()
	initKademlia()
	initMessageSequence()
	initSeedList()
	initMulticastIPv6()
	initBroadcastIPv4()
	initStore()
	initNetwork()

	var err error

	backend.GlobalBlockchainCache = initBlockchainCache(config.BlockchainGlobal, config.CacheMaxBlockSize, config.CacheMaxBlockCount, config.LimitTotalRecords)

	if backend.SearchIndex, err = search.InitSearchIndexStore(config.SearchIndex); err != nil {
		Filters.LogError("Init", "search index '%s' init: %s", config.SearchIndex, err.Error())
	} else {
		backend.userBlockchainUpdateSearchIndex()
	}

	return backend
}

// Connect starts bootstrapping and local peer discovery.
func Connect() {
	go bootstrapKademlia()
	go bootstrap()
	go networks.autoMulticastBroadcast()
	go autoPingAll()
	go networks.networkChangeMonitor()
	go networks.startUPnP()
	go autoBucketRefresh()
}

// The Backend represents an instance of a Peernet client to be used by a frontend.
// Global variables and init functions are to be merged.
type Backend struct {
	GlobalBlockchainCache *BlockchainCache         // Caches blockchains of other peers.
	SearchIndex           *search.SearchIndexStore // Search index of blockchain records.
}

// This variable is to be replaced later by pointers in structures.
var currentBackend *Backend
