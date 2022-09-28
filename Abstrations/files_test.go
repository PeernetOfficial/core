package Abstrations

import (
	"fmt"
	"github.com/PeernetOfficial/core"
	"github.com/PeernetOfficial/core/webapi"
	"github.com/google/uuid"
	"testing"
	"time"
)

func InitPeernet() *core.Backend {
	backendInit, status, err := core.Init("Your application/1.0", "Config.yaml", nil, nil)
	if status != core.ExitSuccess {
		fmt.Printf("Error %d initializing config: %s\n", status, err.Error())
		return nil
	}

	// start config api server
	webapi.Start(backendInit, []string{"127.0.0.1:5125"}, false, "", "", 10*time.Second, 10*time.Second, uuid.Nil)

	backendInit.Connect()

	return backendInit
}

// Testing the touch function if the file is added or not
func TestBackend_Touch(t *testing.T) {
	backend := InitPeernet()
	touch, err := Touch(backend, "Config.yaml")
	if err != nil {
		t.Fail()
	}
	fmt.Printf("blockchain height: %v", touch.BlockchainHeight)
}
