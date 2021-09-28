/*
File Name:  Profile.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"net/http"
	"strconv"

	"github.com/PeernetOfficial/core"
)

// apiProfileData contains profile metadata stored on the blockchain. Any data is treated as untrusted and unverified by default.
type apiProfileData struct {
	Fields []apiBlockRecordProfile `json:"fields"` // All fields
	Status int                     `json:"status"` // Status of the operation, only used when this structure is returned from the API. See core.BlockchainStatusX.
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

Request:    GET /profile/list
Response:   200 with JSON structure apiProfileData
*/
func apiProfileList(w http.ResponseWriter, r *http.Request) {
	fields, status := core.UserProfileList()

	result := apiProfileData{Status: status}
	for n := range fields {
		result.Fields = append(result.Fields, blockRecordProfileToAPI(fields[n]))
	}

	EncodeJSON(w, r, result)
}

/*
apiProfileRead reads a specific users profile field. See core.ProfileX for recognized fields.

Request:    GET /profile/read?field=[index]
Response:   200 with JSON structure apiProfileData
*/
func apiProfileRead(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	fieldN, err1 := strconv.Atoi(r.Form.Get("field"))

	if err1 != nil || fieldN < 0 {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	var result apiProfileData

	var data []byte
	if data, result.Status = core.UserProfileReadField(uint16(fieldN)); result.Status == core.BlockchainStatusOK {
		result.Fields = append(result.Fields, blockRecordProfileToAPI(core.BlockRecordProfile{Type: uint16(fieldN), Data: data}))
	}

	EncodeJSON(w, r, result)
}

/*
apiProfileWrite writes profile fields. See core.ProfileX for recognized fields.

Request:    POST /profile/write with JSON structure apiProfileData
Response:   200 with JSON structure apiBlockchainBlockStatus
*/
func apiProfileWrite(w http.ResponseWriter, r *http.Request) {
	var input apiProfileData
	if err := DecodeJSON(w, r, &input); err != nil {
		return
	}

	var fields []core.BlockRecordProfile

	for n := range input.Fields {
		fields = append(fields, blockRecordProfileFromAPI(input.Fields[n]))
	}

	newHeight, newVersion, status := core.UserProfileWrite(fields)

	EncodeJSON(w, r, apiBlockchainBlockStatus{Status: status, Height: newHeight, Version: newVersion})
}

/*
apiProfileDelete deletes profile fields identified by the types. See core.ProfileX for recognized fields.

Request:    POST /profile/delete with JSON structure apiProfileData
Response:   200 with JSON structure apiBlockchainBlockStatus
*/
func apiProfileDelete(w http.ResponseWriter, r *http.Request) {
	var input apiProfileData
	if err := DecodeJSON(w, r, &input); err != nil {
		return
	}

	var fields []uint16

	for n := range input.Fields {
		fields = append(fields, input.Fields[n].Type)
	}

	newHeight, newVersion, status := core.UserProfileDelete(fields)

	EncodeJSON(w, r, apiBlockchainBlockStatus{Status: status, Height: newHeight, Version: newVersion})
}

// --- conversion from core to API data ---

func blockRecordProfileToAPI(input core.BlockRecordProfile) (output apiBlockRecordProfile) {
	output.Type = input.Type

	switch input.Type {
	case core.ProfileName, core.ProfileEmail, core.ProfileWebsite, core.ProfileTwitter, core.ProfileYouTube, core.ProfileAddress:
		output.Text = input.Text()

	case core.ProfilePicture:
		output.Blob = input.Data

	default:
		output.Blob = input.Data
	}

	return output
}

func blockRecordProfileFromAPI(input apiBlockRecordProfile) (output core.BlockRecordProfile) {
	output.Type = input.Type

	switch input.Type {
	case core.ProfileName, core.ProfileEmail, core.ProfileWebsite, core.ProfileTwitter, core.ProfileYouTube, core.ProfileAddress:
		output.Data = []byte(input.Text)

	case core.ProfilePicture:
		output.Data = input.Blob

	default:
		output.Data = input.Blob
	}

	return output
}
