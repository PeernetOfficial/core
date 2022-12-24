package core

import (
	"encoding/hex"
	"fmt"
	"github.com/PeernetOfficial/core/store"
	"sync"
)

//type BlackList struct {
//	BacklistNodes []*BlackListNode
//}

type BlackListNode struct {
	peer   *PeerInfo
	reason string
}

// BlackListNodeDB blacklist nodes databse
type BlackListNodeDB struct {
	Database store.Store // The database storing the blockchain.
	sync.RWMutex
}

func InitBlackListStoreDB(DatabaseDirectory string) (blackListNodeDB *BlackListNodeDB, err error) {
	if DatabaseDirectory == "" {
		return
	}

	blackListNodeDB = &BlackListNodeDB{}

	if blackListNodeDB.Database, err = store.NewPogrebStore(DatabaseDirectory); err != nil {
		return nil, err
	}
	return blackListNodeDB, err
}

// AddBlackList Adds blacklisted peer
func (backend *Backend) AddBlackList(peerinfo *PeerInfo, reason string) {
	// Store the blacklisted information in the database
	backend.Blacklist.Database.Set(peerinfo.PublicKey.SerializeCompressed(), []byte(reason))

	// Remove the list of nodes if it's present
	backend.PeerlistRemove(peerinfo)
}

// CheckNodeBlackList Checks if the node is blacklisted
func (backend *Backend) CheckNodeBlackList(PublicKey []byte) bool {
	_, found := backend.Blacklist.Database.Get(PublicKey)
	return found
}

// RemoveNodeBlackList Deletes node from the blacklist
func (backend *Backend) RemoveNodeBlackList(PublicKey []byte) {
	backend.Blacklist.Database.Delete(PublicKey)
}

func (backend *Backend) ListAllNodesInBlackList() {
	backend.Blacklist.Database.Iterate(func(key []byte, value []byte) {
		fmt.Println("\nPeer ID: " + hex.EncodeToString(key) + "\n" + "Reason: " + string(value) + "\n" +
			"---------------------------------------------------------------------------\n")
	})
}