package mobile

import (
	"fmt"
	"github.com/PeernetOfficial/core"
	"github.com/PeernetOfficial/core/webapi"
	"github.com/google/uuid"
	"net/http"
	"time"
)

// MobileMain The following function is called as a bind function
// from the Kotlin implementation
func MobileMain(path string) {

	var config core.Config

	// Load the config file
	core.LoadConfig(path+"Config.yaml", &config)

	//Setting modified paths in the config file
	config.SearchIndex = path + "data/search_Index/"
	config.BlockchainGlobal = path + "data/blockchain/"
	config.BlockchainMain = path + "data/blockchain_main/"
	config.WarehouseMain = path + "data/warehouse/"
	config.GeoIPDatabase = path + "data/GeoLite2-City.mmdb"
	config.LogFile = path + "data/log.txt"

	// save modified config changes
	core.SaveConfig(path+"Config.yaml", &config)

	backendInit, status, err := core.Init("Your application/1.0", path+"Config.yaml", nil, nil)
	if status != core.ExitSuccess {
		fmt.Printf("Error %d initializing config: %s\n", status, err.Error())
		return
	}

	// start config api server
	webapi.Start(backendInit, []string{"127.0.0.1:5125"}, false, "", "", 10*time.Second, 10*time.Second, uuid.Nil)

	backendInit.Connect()

	// Checks if the go code can access the internet
	if !connected() {
		fmt.Print("Not connected to the internet ")
	} else {
		fmt.Print("Connected")
	}

}

func connected() (ok bool) {
	_, err := http.Get("http://clients3.google.com/generate_204")
	if err != nil {
		return false
	}
	return true
}
