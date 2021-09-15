/*
File Name:  Status.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"encoding/hex"
	"net/http"

	"github.com/PeernetOfficial/core"
)

func apiTest(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

type apiResponseStatus struct {
	Status        int  `json:"status"`        // Status code: 0 = Ok.
	IsConnected   bool `json:"isconnected"`   // Whether connected to Peernet.
	CountPeerList int  `json:"countpeerlist"` // Count of peers in the peer list. Note that this contains peers that are considered inactive, but have not yet been removed from the list.
	CountNetwork  int  `json:"countnetwork"`  // Count of total peers in the network.
	// This is usually a higher number than CountPeerList, which just represents the current number of connected peers.
	// The CountNetwork number is going to be queried from root peers which may or may not have a limited view.
}

/*
apiStatus returns the current connectivity status to the network
Request:    GET /status
Result:     200 with JSON structure Status
*/
func apiStatus(w http.ResponseWriter, r *http.Request) {
	status := apiResponseStatus{Status: 0, CountPeerList: core.PeerlistCount()}
	status.CountNetwork = status.CountPeerList // For now always same as CountPeerList, until native Statistics message to root peers is available.

	// Connected: If at leat 2 peers.
	// This metric needs to be improved in the future, as root peers never disconnect.
	// Instead, the core should keep a count of "active peers".
	status.IsConnected = status.CountPeerList >= 2

	EncodeJSON(w, r, status)
}

type apiResponsePeerSelf struct {
	PeerID string `json:"peerid"` // Peer ID. This is derived from the public in compressed form.
	NodeID string `json:"nodeid"` // Node ID. This is the blake3 hash of the peer ID and used in the DHT.
}

/*
apiPeerSelf provides information about the self peer details
Request:    GET /peer/self
Result:     200 with JSON structure apiResponsePeerSelf
*/
func apiPeerSelf(w http.ResponseWriter, r *http.Request) {
	response := apiResponsePeerSelf{}
	response.NodeID = hex.EncodeToString(core.SelfNodeID())

	_, publicKey := core.ExportPrivateKey()
	response.PeerID = hex.EncodeToString(publicKey.SerializeCompressed())

	EncodeJSON(w, r, response)
}
