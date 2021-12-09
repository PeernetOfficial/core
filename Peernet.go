/*
File Name:  Peernet.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

var userAgent = "Peernet Core/0.1" // must be overwritten by the caller

// Init initializes the client. The config must be loaded first!
// The User Agent must be provided in the form "Application Name/1.0".
func Init(UserAgent string) {
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
	SqliteSearchIndexMigration()
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
