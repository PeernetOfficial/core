/*
File Name:  Status.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
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
func (api *WebapiInstance) apiStatus(w http.ResponseWriter, r *http.Request) {
	status := apiResponseStatus{Status: 0, CountPeerList: api.backend.PeerlistCount()}
	status.CountNetwork = status.CountPeerList // For now always same as CountPeerList, until native Statistics message to root peers is available.

	// Connected: If at leat 2 peers.
	// This metric needs to be improved in the future, as root peers never disconnect.
	// Instead, the core should keep a count of "active peers".
	status.IsConnected = status.CountPeerList >= 2

	EncodeJSON(api.backend, w, r, status)
}

type apiResponsePeerSelf struct {
	PeerID string `json:"peerid"` // Peer ID. This is derived from the public in compressed form.
	NodeID string `json:"nodeid"` // Node ID. This is the blake3 hash of the peer ID and used in the DHT.
}

/*
apiAccountInfo provides information about the current account.
Request:    GET /account/info
Result:     200 with JSON structure apiResponsePeerSelf
*/
func (api *WebapiInstance) apiAccountInfo(w http.ResponseWriter, r *http.Request) {
	response := apiResponsePeerSelf{}
	response.NodeID = hex.EncodeToString(api.backend.SelfNodeID())

	_, publicKey := api.backend.ExportPrivateKey()
	response.PeerID = hex.EncodeToString(publicKey.SerializeCompressed())

	EncodeJSON(api.backend, w, r, response)
}

/*
apiAccountDelete deletes the current account. The confirm parameter must include the user's choice.
Request:    GET /account/delete?confirm=[0 or 1]
Result:     204 if the user choses not to delete the account

	200 if successfully deleted
*/
func (api *WebapiInstance) apiAccountDelete(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	if confirm, _ := strconv.ParseBool(r.Form.Get("confirm")); !confirm {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	api.backend.DeleteAccount()

	w.WriteHeader(http.StatusOK)
}

/*
apiStatusPeers returns the information about peers currently connected
Request:    GET /status/peers
Result:     200 with JSON array apiResponsePeerInfo
*/
func (api *WebapiInstance) apiStatusPeers(w http.ResponseWriter, r *http.Request) {
	var peers []apiResponsePeerInfo

	// query all nodes
	for _, peer := range api.backend.PeerlistGet() {
		peerInfo := apiResponsePeerInfo{PeerID: hex.EncodeToString(peer.PublicKey.SerializeCompressed()), NodeID: hex.EncodeToString(peer.NodeID)}

		if latitude, longitude, valid := api.Peer2GeoIP(peer); valid {
			peerInfo.GeoIP = fmt.Sprintf("%.4f", latitude) + "," + fmt.Sprintf("%.4f", longitude)
		}

		peers = append(peers, peerInfo)
	}

	EncodeJSON(api.backend, w, r, peers)
}

type apiResponsePeerInfo struct {
	PeerID string `json:"peerid"` // Peer ID. This is derived from the public in compressed form.
	NodeID string `json:"nodeid"` // Node ID. This is the blake3 hash of the peer ID and used in the DHT.
	GeoIP  string `json:"geopi"`  // GeoIP location as "Latitude,Longitude" CSV format. Empty if location not available.
}
