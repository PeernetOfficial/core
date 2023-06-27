/*
File Username:  Profile.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"bytes"
	"net/http"
	"strconv"
	"time"

	"github.com/PeernetOfficial/core/blockchain"
)

// apiProfileData contains profile metadata stored on the blockchain. Any data is treated as untrusted and unverified by default.
type apiProfileData struct {
	Fields []apiBlockRecordProfile `json:"fields"` // All fields
	Status int                     `json:"status"` // Status of the operation, only used when this structure is returned from the API. See blockchain.StatusX.
}

// apiBlockRecordProfile provides information about the end user. Note that all profile data is arbitrary and shall be considered untrusted and unverified.
// To establish trust, the user must load Certificates into the blockchain that validate certain data.
type apiBlockRecordProfile struct {
	Type uint16 `json:"type"` // See ProfileX constants.
	// Depending on the exact type, one of the below fields is used for proper encoding:
	Text string `json:"text"` // Text value. UTF-8 encoding.
	Blob []byte `json:"blob"` // Binary data
}

/*
apiProfileList lists all users profile fields.

Request:    GET /profile/list?node=[nodeid]
Response:   200 with JSON structure apiProfileData
*/
func (api *WebapiInstance) apiProfileList(w http.ResponseWriter, r *http.Request) {

	NodeID, valid := DecodeBlake3Hash(r.URL.Query().Get("node"))

	var fields []blockchain.BlockRecordProfile
	var status int

	result := apiProfileData{Status: status}

	if valid && !bytes.Equal(NodeID, api.Backend.SelfNodeID()) {
		//_, node, _ := api.Backend.FindNode(NodeID, 100)

		_, peers, _ := api.Backend.FindNode(NodeID, time.Second*5)
		// First iteration of the entire blockchain to search for the profile
		// image and Username of the user

		for blockN1 := peers.BlockchainHeight; blockN1 > 0; blockN1-- {
			blockDecoded, _, found, _ := api.Backend.ReadBlock(peers.PublicKey, peers.BlockchainVersion, blockN1)
			if !found {
				continue
			}

			profile, _ := blockchain.DecodeBlockRecordProfile(blockDecoded.Block.RecordsRaw)
			// Adding profile image and Username to the output
			for raw, _ := range profile {

				if profile[raw].Type == blockchain.ProfileName {
					result.Fields = append(result.Fields, blockRecordProfileToAPI(blockchain.BlockRecordProfile{Type: profile[raw].Type, Data: profile[raw].Data[:]}))
				}
				if profile[raw].Type == blockchain.ProfilePicture {
					result.Fields = append(result.Fields, blockRecordProfileToAPI(blockchain.BlockRecordProfile{Type: profile[raw].Type, Data: profile[raw].Data[:]}))
				}
			}
		}

	} else {
		fields, status = api.Backend.UserBlockchain.ProfileList()
		result.Status = status
		for n := range fields {
			result.Fields = append(result.Fields, blockRecordProfileToAPI(fields[n]))
		}
	}

	EncodeJSON(api.Backend, w, r, result)
}

/*
apiProfileRead reads a specific users profile field. See core.ProfileX for recognized fields.

Request:    GET /profile/read?field=[index]&node=[nodeid]
Response:   200 with JSON structure apiProfileData
*/
func (api *WebapiInstance) apiProfileRead(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	fieldN, err1 := strconv.Atoi(r.URL.Query().Get("field"))
	NodeID, valid := DecodeBlake3Hash(r.URL.Query().Get("node"))

	if err1 != nil || fieldN < 0 {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	var result apiProfileData
	var data []byte

	if !valid {
		_, node, _ := api.Backend.FindNode(NodeID, 100)
		if data, result.Status = node.Backend.UserBlockchain.ProfileReadField(uint16(fieldN)); result.Status == blockchain.StatusOK {
			result.Fields = append(result.Fields, blockRecordProfileToAPI(blockchain.BlockRecordProfile{Type: uint16(fieldN), Data: data}))
		}
	} else {
		if api.Backend.NodelistLookup(NodeID) != nil {
			if data, result.Status = api.Backend.NodelistLookup(NodeID).Backend.UserBlockchain.ProfileReadField(uint16(fieldN)); result.Status == blockchain.StatusOK {
				result.Fields = append(result.Fields, blockRecordProfileToAPI(blockchain.BlockRecordProfile{Type: uint16(fieldN), Data: data}))
			}
		}
	}

	EncodeJSON(api.Backend, w, r, result)
}

/*
apiProfileWrite writes profile fields. See core.ProfileX for recognized fields.

Request:    POST /profile/write with JSON structure apiProfileData
Response:   200 with JSON structure apiBlockchainBlockStatus
*/
func (api *WebapiInstance) apiProfileWrite(w http.ResponseWriter, r *http.Request) {
	var input apiProfileData
	if err := DecodeJSON(w, r, &input); err != nil {
		return
	}

	var fields []blockchain.BlockRecordProfile

	for n := range input.Fields {
		fields = append(fields, blockRecordProfileFromAPI(input.Fields[n]))
	}

	newHeight, newVersion, status := api.Backend.UserBlockchain.ProfileWrite(fields)

	api.Backend.LogError("apiProfileWrite", "Height: %v, Version %v", newHeight, newVersion)

	EncodeJSON(api.Backend, w, r, apiBlockchainBlockStatus{Status: status, Height: newHeight, Version: newVersion})
}

/*
apiProfileDelete deletes profile fields identified by the types. See core.ProfileX for recognized fields.

Request:    POST /profile/delete with JSON structure apiProfileData
Response:   200 with JSON structure apiBlockchainBlockStatus
*/
func (api *WebapiInstance) apiProfileDelete(w http.ResponseWriter, r *http.Request) {
	var input apiProfileData
	if err := DecodeJSON(w, r, &input); err != nil {
		return
	}

	var fields []uint16

	for n := range input.Fields {
		fields = append(fields, input.Fields[n].Type)
	}

	newHeight, newVersion, status := api.Backend.UserBlockchain.ProfileDelete(fields)

	EncodeJSON(api.Backend, w, r, apiBlockchainBlockStatus{Status: status, Height: newHeight, Version: newVersion})
}

// --- conversion from core to API data ---

func blockRecordProfileToAPI(input blockchain.BlockRecordProfile) (output apiBlockRecordProfile) {
	output.Type = input.Type

	switch input.Type {
	case blockchain.ProfileName, blockchain.ProfileEmail, blockchain.ProfileWebsite, blockchain.ProfileTwitter, blockchain.ProfileYouTube, blockchain.ProfileAddress:
		output.Text = input.Text()

	case blockchain.ProfilePicture:
		output.Blob = input.Data

	default:
		output.Blob = input.Data
	}

	return output
}

func blockRecordProfileFromAPI(input apiBlockRecordProfile) (output blockchain.BlockRecordProfile) {
	output.Type = input.Type

	switch input.Type {
	case blockchain.ProfileName, blockchain.ProfileEmail, blockchain.ProfileWebsite, blockchain.ProfileTwitter, blockchain.ProfileYouTube, blockchain.ProfileAddress:
		output.Data = []byte(input.Text)

	case blockchain.ProfilePicture:
		output.Data = input.Blob

	default:
		output.Data = input.Blob
	}

	return output
}
