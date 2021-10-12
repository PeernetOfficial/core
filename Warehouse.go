/*
File Name:  Warehouse.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package core

import (
	"github.com/PeernetOfficial/core/warehouse"
)

// UserWarehouse is the user's warehouse for storing files that are shared
var UserWarehouse *warehouse.Warehouse

// folderUserWarehouse is the folder of the user's warehouse
const folderUserWarehouse = "warehouse"

func initUserWarehouse() {
	var err error
	UserWarehouse, err = warehouse.Init(folderUserWarehouse)

	if err != nil {
		Filters.LogError("initUserWarehouse", "error: %s\n", err.Error())
	}
}
