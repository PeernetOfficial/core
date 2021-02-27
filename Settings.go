/*
File Name:  Settings.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

// Version is the current core library version
const Version = "0.1"

const configFile = "Settings.yaml"

var config struct {
	LogFile string `yaml:"LogFile"` // Log file

	Listen        []string `yaml:"Listen"`        // IP:Port combinations
	ListenWorkers int      `yaml:"ListenWorkers"` // Count of workers to process incoming raw packets. Default 2.

	// User specific settings
	PrivateKey string `yaml:"PrivateKey"` // The Private Key, hex encoded so it can be copied manually

	// Initial peer seed list
	SeedList []peerSeed `yaml:"SeedList"`
}

// peerSeed is a singl peer entry from the config's seed list
type peerSeed struct {
	PublicKey string   `yaml:"PublicKey"` // Public key = peer ID. Hex encoded.
	Address   []string `yaml:"Address"`   // IP:Port
}

// loadConfig reads the YAML configuration file
func loadConfig() {
	cfg, err := ioutil.ReadFile(configFile)
	if err != nil {
		// Something went wrong reading config file
		fmt.Printf("Error loading config '%s': %v\n", configFile, err.Error() )
		fmt.Println("Falling back to built-in default settings")

		// Fallback to the built-in parameters
		config.LogFile = "Log.txt"
	}

	// parse config
	err = yaml.Unmarshal(cfg, &config)
	if err != nil {
		fmt.Printf("Configuration file '%s' could not be read. Please make sure it contains valid YAML data. Error: %v\n", configFile, err.Error())
		os.Exit(1)
	}

	// redirect all output to the log file
	logFile, err := os.OpenFile(config.LogFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		fmt.Printf("Error creating log file '%s': %v\n", config.LogFile, err)
	}
	//	defer logFile.Close()	// has to remain open until program closes

	log.SetOutput(logFile)
	log.Printf("---- Peernet Command-Line Client " + Version + " ----\n")
}

func saveConfig() {
	data, err := yaml.Marshal(config)
	if err != nil {
		log.Printf("saveConfig Error marshalling config: %v\n", err.Error())
		return
	}

	err = ioutil.WriteFile(configFile, data, 0644)
	if err != nil {
		log.Printf("saveConfig Error writing config '%s': %v\n", configFile, err.Error())
		return
	}
}
