/*
File Name:  Peernet.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

var userAgent = "Peernet Core/0.1" // must be overwritten by the caller

// Init initializes the client. The config must be loaded first!
// The User Agent must be provided in the form "Application Name/1.0".
func Init(UserAgent string) (backend *Backend) {
	if userAgent = UserAgent; userAgent == "" {
		return
	}

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

	backend = &Backend{}
	backend.GlobalBlockchainCache = initBlockchainCache(config.BlockchainGlobal, config.CacheMaxBlockSize, config.CacheMaxBlockCount, config.LimitTotalRecords)

	currentBackend = backend

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
	GlobalBlockchainCache *BlockchainCache // stores other peers blockchains
}

// This variable is to be replaced later by pointers in structures.
var currentBackend *Backend
