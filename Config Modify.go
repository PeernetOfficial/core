/*
File Name:  Config Modify.go
Copyright:  2021 Peernet s.r.o.
Author:     Akilan Selvacoumar
*/

package core

// ModifyConfig public type of the config struct
type ModifyConfig struct {
	// Locations of important files and folders
	LogFile          string `yaml:"LogFile"`          // Log file. It contains informational and error messages.
	BlockchainMain   string `yaml:"BlockchainMain"`   // Blockchain main stores the end-users blockchain data. It contains meta data of shared files, profile data, and social interactions.
	BlockchainGlobal string `yaml:"BlockchainGlobal"` // Blockchain global caches blockchain data from global users. Empty to disable.
	WarehouseMain    string `yaml:"WarehouseMain"`    // Warehouse main stores the actual data of files shared by the end-user.
	SearchIndex      string `yaml:"SearchIndex"`      // Local search index of blockchain records. Empty to disable.
	GeoIPDatabase    string `yaml:"GeoIPDatabase"`    // GeoLite2 City database to provide GeoIP information.

	// Listen settings
	Listen        []string `yaml:"Listen"`        // IP:Port combinations
	ListenWorkers int      `yaml:"ListenWorkers"` // Count of workers to process incoming raw packets. Default 2.

	// User specific settings
	PrivateKey string `yaml:"PrivateKey"` // The Private Key, hex encoded so it can be copied manually

	// Initial peer seed list
	SeedList           []peerSeed `yaml:"SeedList"`
	AutoUpdateSeedList bool       `yaml:"AutoUpdateSeedList"`
	SeedListVersion    int        `yaml:"SeedListVersion"`

	// Connection settings
	EnableUPnP    bool `yaml:"EnableUPnP"`    // Enables support for UPnP.
	LocalFirewall bool `yaml:"LocalFirewall"` // Indicates that a local firewall may drop unsolicited incoming packets.

	// PortForward specifies an external port that was manually forwarded by the user. All listening IPs must have that same port number forwarded!
	// If this setting is invalid, it will prohibit other peers from connecting. If set, it automatically disables UPnP.
	PortForward uint16 `yaml:"PortForward"`

	// Global blockchain cache limits
	CacheMaxBlockSize  uint64 `yaml:"CacheMaxBlockSize"`  // Max block size to accept in bytes.
	CacheMaxBlockCount uint64 `yaml:"CacheMaxBlockCount"` // Max block count to cache per peer.
	LimitTotalRecords  uint64 `yaml:"LimitTotalRecords"`  // Record count limit. 0 = unlimited. Max Records * Max Block Size = Size Limit.
}

// ModifyConfig Function call to modify config
func (modifyConfig *ModifyConfig) ModifyConfig() {

	if modifyConfig.LogFile != "" {
		config.LogFile = modifyConfig.LogFile
	}
	if modifyConfig.BlockchainMain != "" {
		config.BlockchainMain = modifyConfig.BlockchainMain
	}
	if modifyConfig.BlockchainGlobal != "" {
		config.BlockchainGlobal = modifyConfig.BlockchainGlobal
	}
	if modifyConfig.WarehouseMain != "" {
		config.WarehouseMain = modifyConfig.WarehouseMain
	}
	// Empty could be used to disable the search hence
	// it is mandatory mention "-1" when the user would
	// want to keep the searchIndex to default
	if modifyConfig.SearchIndex != "-1" {
		config.SearchIndex = modifyConfig.SearchIndex
	}
	if modifyConfig.GeoIPDatabase != "" {
		config.GeoIPDatabase = modifyConfig.GeoIPDatabase
	}
	if len(modifyConfig.Listen) != 0 {
		config.Listen = modifyConfig.Listen
	}
	if modifyConfig.ListenWorkers != 0 {
		config.ListenWorkers = modifyConfig.ListenWorkers
	}
	if modifyConfig.PrivateKey != "" {
		config.PrivateKey = modifyConfig.PrivateKey
	}
	if len(modifyConfig.SeedList) != 0 {
		config.SeedList = modifyConfig.SeedList
	}
	if modifyConfig.AutoUpdateSeedList == false {
		config.AutoUpdateSeedList = false
	}
	if modifyConfig.SeedListVersion != 0 {
		config.SeedListVersion = modifyConfig.SeedListVersion
	}
	if modifyConfig.EnableUPnP == false {
		config.EnableUPnP = false
	}
	if modifyConfig.LocalFirewall == true {
		config.LocalFirewall = true
	}
	if modifyConfig.PortForward != 0 {
		config.PortForward = modifyConfig.PortForward
	}
	if modifyConfig.CacheMaxBlockSize != 0 {
		config.CacheMaxBlockSize = modifyConfig.CacheMaxBlockSize
	}
	if modifyConfig.CacheMaxBlockCount != 0 {
		config.CacheMaxBlockCount = modifyConfig.CacheMaxBlockCount
	}
	if modifyConfig.LimitTotalRecords != 0 {
		config.LimitTotalRecords = modifyConfig.LimitTotalRecords
	}
	
	// write to the config the results saved
	saveConfig()
}
