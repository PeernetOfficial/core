/*
File Name:  Settings.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	_ "embed" // Required for embedding default Config file
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"

	"gopkg.in/yaml.v3"
)

// Version is the current core library version
const Version = "Alpha 6/28.12.2021"

// Config defines the minimum required config for a Peernet client.
type Config struct {
	// Locations of important files and folders
	LogFile          string `yaml:"LogFile"`          // Log file. It contains informational and error messages.
	BlockchainMain   string `yaml:"BlockchainMain"`   // Blockchain main stores the end-users blockchain data. It contains meta data of shared files, profile data, and social interactions.
	BlockchainGlobal string `yaml:"BlockchainGlobal"` // Blockchain global caches blockchain data from global users. Empty to disable.
	WarehouseMain    string `yaml:"WarehouseMain"`    // Warehouse main stores the actual data of files shared by the end-user.
	SearchIndex      string `yaml:"SearchIndex"`      // Local search index of blockchain records. Empty to disable.
	GeoIPDatabase    string `yaml:"GeoIPDatabase"`    // GeoLite2 City database to provide GeoIP information.

	// Target for the log messages: 0 = Log file,  1 = Stdout, 2 = Log file + Stdout, 3 = None
	LogTarget int `yaml:"LogTarget"`

	// Listen settings
	Listen            []string `yaml:"Listen"`            // IP:Port combinations
	ListenWorkers     int      `yaml:"ListenWorkers"`     // Count of workers to process incoming raw packets. Default 2.
	ListenWorkersLite int      `yaml:"ListenWorkersLite"` // Count of workers to process incoming lite packets. Default 2.

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

// peerSeed is a singl peer entry from the config's seed list
type peerSeed struct {
	PublicKey string   `yaml:"PublicKey"` // Public key = peer ID. Hex encoded.
	Address   []string `yaml:"Address"`   // IP:Port
}

//go:embed "Config Default.yaml"
var defaultConfig []byte

// LoadConfig reads the YAML configuration file and unmarshals it into the provided structure.
// If the config file does not exist or is empty, it will fall back to the default config which is hardcoded.
// Status is of type ExitX.
func LoadConfig(Filename string, ConfigOut interface{}) (status int, err error) {
	var configData []byte

	// check if the file is non existent or empty
	stats, err := os.Stat(Filename)
	if err != nil && os.IsNotExist(err) || err == nil && stats.Size() == 0 {
		configData = defaultConfig
	} else if err != nil {
		return ExitErrorConfigAccess, err
	} else if configData, err = ioutil.ReadFile(Filename); err != nil {
		return ExitErrorConfigRead, err
	}

	// parse the config
	err = yaml.Unmarshal(configData, ConfigOut)
	if err != nil {
		return ExitErrorConfigParse, err
	}

	return ExitSuccess, nil
}

// SaveConfig stores the config.
func SaveConfig(Filename string, Config interface{}) (err error) {
	data, err := yaml.Marshal(Config)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(Filename, data, 0666)
}

// SaveConfig stores the current runtime config to file. Any foreign settings not present in the Config structure will be deleted.
func (backend *Backend) SaveConfig() {
	if err := SaveConfig(backend.ConfigFilename, *backend.Config); err != nil {
		backend.LogError("SaveConfig", "writing config '%s': %v\n", backend.ConfigFilename, err.Error())
	}
}

func (backend *Backend) configUpdateSeedList() {
	// parse the embedded config
	var configD Config
	if err := yaml.Unmarshal(defaultConfig, &configD); err != nil {
		return
	}

	// check if the seed list needs an update
	if backend.Config.SeedListVersion < configD.SeedListVersion {
		backend.Config.SeedList = configD.SeedList
		backend.Config.SeedListVersion = configD.SeedListVersion
		backend.SaveConfig()
	}
}

// InitLog redirects subsequent log messages into the default log file specified in the configuration
func (backend *Backend) initLog() (err error) {
	// create the directory to the log file if specified
	if directory, _ := path.Split(backend.Config.LogFile); directory != "" {
		os.MkdirAll(directory, os.ModePerm)
	}

	logFile, err := os.OpenFile(backend.Config.LogFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666) // 666 : All uses can read/write
	if err != nil {
		return err
	}
	//defer logFile.Close()	// has to remain open until program closes

	log.SetOutput(logFile)
	log.Printf("---- Peernet Command-Line Client " + Version + " ----\n")

	return nil
}

// Logs an error message.
func (backend *Backend) LogError(function, format string, v ...interface{}) {
	switch backend.Config.LogTarget {
	case 0:
		log.Printf("["+function+"] "+format, v...)

	case 1:
		fmt.Fprintf(backend.Stdout, "["+function+"] "+format, v...)

	case 2:
		log.Printf("["+function+"] "+format, v...)

		fmt.Fprintf(backend.Stdout, "["+function+"] "+format, v...)

	case 3: // None
	}

	backend.Filters.LogError(function, format, v)
}
