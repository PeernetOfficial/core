/*
File Name:  Peernet.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"sync"

	"github.com/PeernetOfficial/core/blockchain"
	"github.com/PeernetOfficial/core/btcec"
	"github.com/PeernetOfficial/core/dht"
	"github.com/PeernetOfficial/core/protocol"
	"github.com/PeernetOfficial/core/search"
	"github.com/PeernetOfficial/core/store"
	"github.com/PeernetOfficial/core/warehouse"
)

// Init initializes the client. If the config file does not exist or is empty, a default one will be created.
// The User Agent must be provided in the form "Application Name/1.0".
// The returned status is of type ExitX. Anything other than ExitSuccess indicates a fatal failure.
func Init(UserAgent string, ConfigFilename string, Filters *Filters, ConfigOut interface{}) (backend *Backend, status int, err error) {
	if UserAgent == "" {
		return
	}

	backend = &Backend{
		ConfigFilename: ConfigFilename,
		userAgent:      UserAgent,
		Stdout:         newMultiWriter(),
	}

	if Filters != nil {
		backend.Filters = *Filters
	}

	// The configuration and log init are fatal events if they fail.
	if status, err = LoadConfig(ConfigFilename, &backend.Config); status != ExitSuccess {
		return nil, status, err
	}
	if ConfigOut != nil {
		if status, err = LoadConfig(ConfigFilename, ConfigOut); status != ExitSuccess {
			return nil, status, err
		}
		backend.ConfigClient = ConfigOut
	}

	if err = backend.initLog(); err != nil {
		return nil, ExitErrorLogInit, err
	}

	backend.initFilters()
	backend.initPeerID()
	backend.initUserBlockchain()
	backend.initUserWarehouse()
	backend.initKademlia()
	backend.initMessageSequence()
	backend.initSeedList()
	initMulticastIPv6()
	initBroadcastIPv4()
	backend.initStore()
	backend.initNetwork()
	backend.initBlockchainCache()

	if backend.SearchIndex, err = search.InitSearchIndexStore(backend.Config.SearchIndex); err != nil {
		backend.LogError("Init", "search index '%s' init: %s", backend.Config.SearchIndex, err.Error())
	} else {
		backend.userBlockchainUpdateSearchIndex()
	}

	return backend, ExitSuccess, nil
}

// Connect starts bootstrapping and local peer discovery.
func (backend *Backend) Connect() {
	go backend.bootstrapKademlia()
	go backend.bootstrap()
	go backend.networks.autoMulticastBroadcast()
	go backend.autoPingAll()
	go backend.networks.networkChangeMonitor()
	go backend.networks.startUPnP()
	go backend.autoBucketRefresh()
}

// The Backend represents an instance of a Peernet client to be used by a frontend.
// Global variables and init functions are to be merged.
type Backend struct {
	ConfigFilename        string                   // Filename of the configuration file.
	Config                *Config                  // Core configuration
	ConfigClient          interface{}              // Custom configuration from the client
	Filters               Filters                  // Filters allow to install hooks.
	userAgent             string                   // User Agent
	GlobalBlockchainCache *BlockchainCache         // Caches blockchains of other peers.
	SearchIndex           *search.SearchIndexStore // Search index of blockchain records.
	networks              *Networks                // All connected networks.
	dhtStore              store.Store              // dhtStore contains all key-value data served via DHT
	UserBlockchain        *blockchain.Blockchain   // UserBlockchain is the user's blockchain and exports functions to directly read and write it
	UserWarehouse         *warehouse.Warehouse     // UserWarehouse is the user's warehouse for storing files that are shared
	nodesDHT              *dht.DHT                 // Nodes connected in the DHT.

	// peerID is the current peer's ID. It is a ECDSA (secp256k1) 257-bit public key.
	PeerPrivateKey *btcec.PrivateKey
	PeerPublicKey  *btcec.PublicKey

	// The node ID is the blake3 hash of the public key compressed form.
	nodeID []byte

	// peerList keeps track of all peers
	peerList      map[[btcec.PubKeyBytesLenCompressed]byte]*PeerInfo
	peerlistMutex sync.RWMutex

	// nodeList is a mirror of peerList but using the node ID
	nodeList map[[protocol.HashSize]byte]*PeerInfo

	// peerMonitor is a list of channels receiving information about new peers
	peerMonitor []chan<- *PeerInfo

	// Stdout bundles any output for the end-user. Writers may subscribe/unsubscribe.
	Stdout *multiWriter
}
