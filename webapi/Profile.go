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
	Fields []apiBlockRecordProfileField `json:"fields"` // All fields
	Blobs  []apiBlockRecordProfileBlob  `json:"blobs"`  // All blobs
	Status int                          `json:"status"` // Status of the operation, only used when this structure is returned from the API. See core.BlockchainStatusX.
}

/*
apiProfileList lists all users profile fields and blobs.

Request:    GET /profile/list
Response:   200 with JSON structure apiProfileData
*/
func apiProfileList(w http.ResponseWriter, r *http.Request) {
	fields, blobs, status := core.UserProfileList()

	result := apiProfileData{Status: status}
	for n := range fields {
		result.Fields = append(result.Fields, apiBlockRecordProfileField{Type: fields[n].Type, Text: fields[n].Text})
	}
	for n := range blobs {
		result.Blobs = append(result.Blobs, apiBlockRecordProfileBlob{Type: blobs[n].Type, Data: blobs[n].Data})
	}

	apiEncodeJSON(w, r, result)
}

/*
apiProfileRead reads a specific users profile field or blob.
For the index see core.ProfileFieldX and core.ProfileBlobX constants.

Request:    GET /profile/read?field=[index] or &blob=[index]
Response:   200 with JSON structure apiProfileData
*/
func apiProfileRead(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	fieldN, err1 := strconv.Atoi(r.Form.Get("field"))
	blobN, err2 := strconv.Atoi(r.Form.Get("blob"))

	if (err1 != nil && err2 != nil) || (err1 == nil && fieldN < 0) || (err2 == nil && blobN < 0) {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	var result apiProfileData

	if err1 == nil {
		var text string
		if text, result.Status = core.UserProfileReadField(uint16(fieldN)); result.Status == core.BlockchainStatusOK {
			result.Fields = append(result.Fields, apiBlockRecordProfileField{Type: uint16(fieldN), Text: text})
		}
	} else {
		var data []byte
		if data, result.Status = core.UserProfileReadBlob(uint16(blobN)); result.Status == core.BlockchainStatusOK {
			result.Blobs = append(result.Blobs, apiBlockRecordProfileBlob{Type: uint16(blobN), Data: data})
		}
	}

	apiEncodeJSON(w, r, result)
}

/*
apiProfileWrite writes a specific users profile field or blob.
For the index see core.ProfileFieldX and core.ProfileBlobX constants.

Request:    POST /profile/write with JSON structure apiProfileData
Response:   200 with JSON structure apiBlockchainBlockStatus
*/
func apiProfileWrite(w http.ResponseWriter, r *http.Request) {
	var input apiProfileData
	if err := apiDecodeJSON(w, r, &input); err != nil {
		return
	}

	var profile core.BlockRecordProfile

	for n := range input.Fields {
		profile.Fields = append(profile.Fields, core.BlockRecordProfileField{Type: input.Fields[n].Type, Text: input.Fields[n].Text})
	}
	for n := range input.Blobs {
		profile.Blobs = append(profile.Blobs, core.BlockRecordProfileBlob{Type: input.Blobs[n].Type, Data: input.Blobs[n].Data})
	}

	newHeight, status := core.UserProfileWrite(profile)

	apiEncodeJSON(w, r, apiBlockchainBlockStatus{Status: status, Height: newHeight})
}

// --- conversion from core to API data ---

func blockRecordProfileToAPI(input core.BlockRecordProfile) (output apiBlockRecordProfile) {
	for n := range input.Fields {
		output.Fields = append(output.Fields, apiBlockRecordProfileField{Type: input.Fields[n].Type, Text: input.Fields[n].Text})
	}
	for n := range input.Blobs {
		output.Blobs = append(output.Blobs, apiBlockRecordProfileBlob{Type: input.Blobs[n].Type, Data: input.Blobs[n].Data})
	}

	return output
}

func blockRecordProfileFromAPI(input apiBlockRecordProfile) (output core.BlockRecordProfile) {
	for n := range input.Fields {
		output.Fields = append(output.Fields, core.BlockRecordProfileField{Type: input.Fields[n].Type, Text: input.Fields[n].Text})
	}
	for n := range input.Blobs {
		output.Blobs = append(output.Blobs, core.BlockRecordProfileBlob{Type: input.Blobs[n].Type, Data: input.Blobs[n].Data})
	}

	return output
}
