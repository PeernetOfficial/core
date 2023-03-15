package search

import (
	"github.com/PeernetOfficial/core/protocol"
	"github.com/google/uuid"
)

// SearchNodeIDBasedOnHash Provides a list of NodeIDs
// based on the hash provided
// This is used to find out which nodes are hosting
// which files based on the hash provided
func (index *SearchIndexStore) SearchNodeIDBasedOnHash(hash []byte) (NodeIDs [][]byte, err error) {
	var resultMap map[uuid.UUID]*SearchIndexRecord
	err = index.LookupHash(SearchSelector{Hash: hash}, resultMap)
	if err != nil {
		return
	}

	for i := range resultMap {
		NodeIDs = append(NodeIDs, protocol.PublicKey2NodeID(resultMap[i].PublicKey))
	}

	return
}
