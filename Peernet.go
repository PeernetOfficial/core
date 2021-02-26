/*
File Name:  Peernet.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package core

func init() {
	loadConfig()
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
}
