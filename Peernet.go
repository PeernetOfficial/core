/*
File Name:  Peernet.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package core

// Init initializes the client. The config must be loaded first!
func Init() {
	initPeerID()
	initMulticastIPv6()
	initBroadcastIPv4()
	initNetwork()
	initSeedList()
}

// Connect starts bootstrapping and local peer discovery.
func Connect() {
	go bootstrap()
	go autoMulticastBroadcast()
	go autoPingAll()
	go networkChangeMonitor()
}
