/*
File Name:  Peernet.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

// Init initializes the client. The config must be loaded first!
func Init() {
	initPeerID()
	initKademlia()
	initMessageSequence()
	initSeedList()
	initMulticastIPv6()
	initBroadcastIPv4()
	initStore()
	initNetwork()
}

// Connect starts bootstrapping and local peer discovery.
func Connect() {
	go bootstrapKademlia()
	go bootstrap()
	go autoMulticastBroadcast()
	go autoPingAll()
	go networkChangeMonitor()
	go autoBucketRefresh()
}
