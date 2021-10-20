/*
File Name:  Settings.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	_ "embed" // Required for embedding default Config file
	"io/ioutil"
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

// Version is the current core library version
const Version = "Alpha 4/20.10.2021"

var config struct {
	LogFile string `yaml:"LogFile"` // Log file

	Listen        []string `yaml:"Listen"`        // IP:Port combinations
	ListenWorkers int      `yaml:"ListenWorkers"` // Count of workers to process incoming raw packets. Default 2.

	// User specific settings
	PrivateKey string `yaml:"PrivateKey"` // The Private Key, hex encoded so it can be copied manually

	// Initial peer seed list
	SeedList           []peerSeed `yaml:"SeedList"`
	AutoUpdateSeedList bool       `yaml:"AutoUpdateSeedList"`
	SeedListVersion    int        `yaml:"SeedListVersion"`

	// Connection settings
	EnableUPnP bool `yaml:"EnableUPnP"` // Enables support for UPnP.

	// PortForward specifies an external port that was manually forwarded by the user. All listening IPs must have that same port number forwarded!
	// If this setting is invalid, it will prohibit other peers from connecting. If set, it automatically disables UPnP.
	PortForward uint16 `yaml:"PortForward"`
}

// peerSeed is a singl peer entry from the config's seed list
type peerSeed struct {
	PublicKey string   `yaml:"PublicKey"` // Public key = peer ID. Hex encoded.
	Address   []string `yaml:"Address"`   // IP:Port
}

var configFile string

//go:embed "Config Default.yaml"
var defaultConfig []byte

// LoadConfig reads the YAML configuration file. If an error is returned, the application shall exit.
// Status: 0 = Unknown error checking config file, 1 = Error reading config file, 2 = Error parsing config file, 3 = Success
func LoadConfig(filename string) (status int, err error) {
	var configData []byte
	configFile = filename

	// check if the file is non existent or empty
	stats, err := os.Stat(filename)
	if err != nil && os.IsNotExist(err) || err == nil && stats.Size() == 0 {
		configData = defaultConfig
	} else if err != nil {
		return 0, err
	} else if configData, err = ioutil.ReadFile(filename); err != nil {
		return 1, err
	}

	// parse the config
	err = yaml.Unmarshal(configData, &config)
	if err != nil {
		return 2, err
	}

	return 3, nil
}

// LoadConfigOut is similar to LoadConfig but unmarshals the config into a caller provided structure.
func LoadConfigOut(Filename string, ConfigOut interface{}) (status int, err error) {
	var configData []byte
	configFile = Filename

	// check if the file is non existent or empty
	stats, err := os.Stat(Filename)
	if err != nil && os.IsNotExist(err) || err == nil && stats.Size() == 0 {
		configData = defaultConfig
	} else if err != nil {
		return 0, err
	} else if configData, err = ioutil.ReadFile(Filename); err != nil {
		return 1, err
	}

	// parse the config
	if err = yaml.Unmarshal(configData, &config); err != nil {
		return 2, err
	}
	if err = yaml.Unmarshal(configData, ConfigOut); err != nil {
		return 2, err
	}

	return 3, nil
}

func saveConfig() {
	data, err := yaml.Marshal(config)
	if err != nil {
		Filters.LogError("saveConfig", "marshalling config: %v\n", err.Error())
		return
	}

	err = ioutil.WriteFile(configFile, data, 0644)
	if err != nil {
		Filters.LogError("saveConfig", "writing config '%s': %v\n", configFile, err.Error())
		return
	}
}

// InitLog redirects subsequent log messages into the default log file specified in the configuration
func InitLog() (err error) {
	logFile, err := os.OpenFile(config.LogFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		return err
	}
	//defer logFile.Close()	// has to remain open until program closes

	log.SetOutput(logFile)
	log.Printf("---- Peernet Command-Line Client " + Version + " ----\n")

	return nil
}

func configUpdateSeedList() {
	// parse the embedded config
	configD := config
	if err := yaml.Unmarshal(defaultConfig, &configD); err != nil {
		return
	}

	if config.SeedListVersion < configD.SeedListVersion {
		config.SeedList = configD.SeedList
		config.SeedListVersion = configD.SeedListVersion
		saveConfig()
	}
}
