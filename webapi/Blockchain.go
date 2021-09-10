/*
File Name:  Blockchain.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"encoding/hex"
	"net/http"
	"strconv"
	"time"

	"github.com/PeernetOfficial/core"
	"github.com/google/uuid"
)

const dateFormat = "2006-01-02 15:04:05"

type apiBlockchainHeader struct {
	PeerID  string `json:"peerid"`  // Peer ID hex encoded.
	Version uint64 `json:"version"` // Current version number of the blockchain.
	Height  uint64 `json:"height"`  // Height of the blockchain (number of blocks). If 0, no data exists.
}

/*
apiBlockchainSelfHeader returns the current blockchain header information

Request:    GET /blockchain/self/header
Result:     200 with JSON structure apiResponsePeerSelf
*/
func apiBlockchainSelfHeader(w http.ResponseWriter, r *http.Request) {
	publicKey, height, version := core.UserBlockchainHeader()

	apiEncodeJSON(w, r, apiBlockchainHeader{Version: version, Height: height, PeerID: hex.EncodeToString(publicKey.SerializeCompressed())})
}

type apiBlockRecordRaw struct {
	Type uint8  `json:"type"` // Record Type. See core.RecordTypeX.
	Data []byte `json:"data"` // Data according to the type.
}

// apiBlockchainBlockRaw contains a raw block of the blockchain via API
type apiBlockchainBlockRaw struct {
	Records []apiBlockRecordRaw `json:"records"` // Block records in encoded raw format.
}

type apiBlockchainBlockStatus struct {
	Status int    `json:"status"` // Status: 0 = Success, 1 = Error invalid data
	Height uint64 `json:"height"` // New height of the blockchain (number of blocks).
}

/*
apiBlockchainSelfAppend appends a block to the blockchain. This is a low-level function for already encoded blocks.
Do not use this function. Adding invalid data to the blockchain may corrupt it which might result in blacklisting by other peers.

Request:    POST /blockchain/self/append with JSON structure apiBlockchainBlockRaw
Response:   200 with JSON structure apiBlockchainBlockStatus
*/
func apiBlockchainSelfAppend(w http.ResponseWriter, r *http.Request) {
	var input apiBlockchainBlockRaw
	if err := apiDecodeJSON(w, r, &input); err != nil {
		return
	}

	var records []core.BlockRecordRaw

	for _, record := range input.Records {
		records = append(records, core.BlockRecordRaw{Type: record.Type, Data: record.Data})
	}

	newHeight, status := core.UserBlockchainAppend(records)

	apiEncodeJSON(w, r, apiBlockchainBlockStatus{Status: status, Height: newHeight})
}

type apiBlockchainBlock struct {
	Status            int                 `json:"status"`            // Status: 0 = Success, 1 = Error block not found, 2 = Error block encoding (indicates that the blockchain is corrupt)
	PeerID            string              `json:"peerid"`            // Peer ID hex encoded.
	LastBlockHash     []byte              `json:"lastblockhash"`     // Hash of the last block. Blake3.
	BlockchainVersion uint64              `json:"blockchainversion"` // Blockchain version
	Number            uint64              `json:"blocknumber"`       // Block number
	RecordsRaw        []apiBlockRecordRaw `json:"recordsraw"`        // Records raw. Successfully decoded records are parsed into the below fields.
	RecordsDecoded    []interface{}       `json:"recordsdecoded"`    // Records decoded. The encoding for each record depends on its type.
}

// apiBlockRecordProfile contains end-user information. Any data is treated as untrusted and unverified by default.
type apiBlockRecordProfile struct {
	Fields []apiBlockRecordProfileField `json:"fields"` // All fields
	Blobs  []apiBlockRecordProfileBlob  `json:"blobs"`  // Blobs
}

// apiBlockRecordProfileField contains a single information about the end user. The data is always UTF8 text encoded.
// Note that all profile data is arbitrary and shall be considered untrusted and unverified.
// To establish trust, the user must load Certificates into the blockchain that validate certain data.
type apiBlockRecordProfileField struct {
	Type uint16 `json:"type"` // See ProfileFieldX constants.
	Text string `json:"text"` // The data
}

// apiBlockRecordProfileBlob is similar to apiBlockRecordProfileField but contains binary objects instead of text.
// It can be used for example to store a profile picture on the blockchain.
type apiBlockRecordProfileBlob struct {
	Type uint16 `json:"type"` // See ProfileBlobX constants.
	Data []byte `json:"data"` // The data
}

// apiFileMetadata describes recognized metadata that is decoded into text.
type apiFileMetadata struct {
	Type  uint16 `json:"type"`  // See core.TagTypeX constants.
	Name  string `json:"name"`  // User friendly name of the tag. Use the Type fields to identify the metadata as this name may change.
	Value string `json:"value"` // Text value of the tag.
}

// apiFileTagRaw describes a raw tag. This allows to support future metadata that is not yet defined in the core library.
type apiFileTagRaw struct {
	Type uint16 `json:"type"` // See core.TagTypeX constants.
	Data []byte `json:"data"` // Data
}

// apiBlockRecordFile is the metadata of a file published on the blockchain
type apiBlockRecordFile struct {
	ID          uuid.UUID         `json:"id"`          // Unique ID.
	Hash        []byte            `json:"hash"`        // Blake3 hash of the file data
	Type        uint8             `json:"type"`        // Type (low-level)
	Format      uint16            `json:"format"`      // Format (high-level)
	Size        uint64            `json:"size"`        // Size of the file
	Folder      string            `json:"folder"`      // Folder, optional
	Name        string            `json:"name"`        // Name of the file
	Description string            `json:"description"` // Description. This is expected to be multiline and contain hashtags!
	Date        time.Time         `json:"date"`        // Date of the virtual file
	Metadata    []apiFileMetadata `json:"metadata"`    // Metadata. These are decoded tags.
	TagsRaw     []apiFileTagRaw   `json:"tagsraw"`     // All tags encoded that were not recognized as metadata.

	// The following known tags from the core library are decoded into metadata or other fields in above structure; everything else is a raw tag:
	// TagTypeName, TagTypeFolder, TagTypeDescription, TagTypeDateCreated
	// The caller can specify its own metadata fields and fill the TagsRaw structure when creating a new file. It will be returned when reading the files' data.
}

/*
apiBlockchainSelfRead reads a block and returns the decoded information.

Request:    GET /blockchain/self/read?block=[number]
Result:     200 with JSON structure apiBlockchainBlock
*/
func apiBlockchainSelfRead(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	blockN, err := strconv.Atoi(r.Form.Get("block"))
	if err != nil || blockN < 0 {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	block, status, _ := core.UserBlockchainRead(uint64(blockN))
	result := apiBlockchainBlock{Status: status}

	if status == 0 {
		for _, record := range block.RecordsRaw {
			result.RecordsRaw = append(result.RecordsRaw, apiBlockRecordRaw{Type: record.Type, Data: record.Data})
		}

		result.PeerID = hex.EncodeToString(block.OwnerPublicKey.SerializeCompressed())

		for _, record := range block.RecordsDecoded {
			switch v := record.(type) {
			case core.BlockRecordFile:
				result.RecordsDecoded = append(result.RecordsDecoded, blockRecordFileToAPI(v))

			case core.BlockRecordProfile:
				result.RecordsDecoded = append(result.RecordsDecoded, blockRecordProfileToAPI(v))

			}
		}
	}

	apiEncodeJSON(w, r, result)
}

// apiBlockAddFiles contains the file metadata to add to the blockchain
type apiBlockAddFiles struct {
	Files  []apiBlockRecordFile `json:"files"`  // List of files
	Status int                  `json:"status"` // Status of the operation, only used when this structure is returned from the API.
}

/*
apiBlockchainSelfAddFile adds a file with the provided information to the blockchain.

Request:    POST /blockchain/self/add/file with JSON structure apiBlockAddFiles
Response:   200 with JSON structure apiBlockchainBlockStatus
*/
func apiBlockchainSelfAddFile(w http.ResponseWriter, r *http.Request) {
	var input apiBlockAddFiles
	if err := apiDecodeJSON(w, r, &input); err != nil {
		return
	}

	var filesAdd []core.BlockRecordFile

	for _, file := range input.Files {
		if file.ID == uuid.Nil { // if the ID is not provided by the caller, set it
			file.ID = uuid.New()
		}

		filesAdd = append(filesAdd, blockRecordFileFromAPI(file))
	}

	newHeight, status := core.UserBlockchainAddFiles(filesAdd)

	apiEncodeJSON(w, r, apiBlockchainBlockStatus{Status: status, Height: newHeight})
}

/*
apiBlockchainSelfListFile lists all files stored on the blockchain.

Request:    GET /blockchain/self/list/file
Response:   200 with JSON structure apiBlockAddFiles
*/
func apiBlockchainSelfListFile(w http.ResponseWriter, r *http.Request) {
	files, status := core.UserBlockchainListFiles()

	var result apiBlockAddFiles

	for _, file := range files {
		result.Files = append(result.Files, blockRecordFileToAPI(file))
	}

	result.Status = status

	apiEncodeJSON(w, r, result)
}

// --- conversion from core to API data ---

func isFileTagKnownMetadata(tagType uint16) bool {
	switch tagType {
	case core.TagTypeName, core.TagTypeFolder, core.TagTypeDescription, core.TagTypeDateCreated, core.TagTypeDateShared:
		return true

	default:
		return false
	}
}

func blockRecordFileToAPI(input core.BlockRecordFile) (output apiBlockRecordFile) {
	output = apiBlockRecordFile{ID: input.ID, Hash: input.Hash, Type: input.Type, Format: input.Format, Size: input.Size, TagsRaw: []apiFileTagRaw{}, Metadata: []apiFileMetadata{}}

	// Copy all raw tags. This allows the API caller to decode any future tags that are not defined yet.
	for n := range input.TagsRaw {
		if !isFileTagKnownMetadata(input.TagsRaw[n].Type) {
			output.TagsRaw = append(output.TagsRaw, apiFileTagRaw{Type: input.TagsRaw[n].Type, Data: input.TagsRaw[n].Data})
		}
	}

	// Try to decode tags into known metadata.
	for _, tagDecoded := range input.TagsDecoded {
		switch v := tagDecoded.(type) {
		case core.FileTagName:
			output.Name = v.Name

		case core.FileTagFolder:
			output.Folder = v.Name

		case core.FileTagDescription:
			output.Description = v.Description

		case core.FileTagDateCreated:
			output.Metadata = append(output.Metadata, apiFileMetadata{Type: core.TagTypeDateCreated, Name: "Date Created", Value: v.Date.Format(dateFormat)})

		case core.FileTagDateShared:
			output.Date = v.Date

		}
	}

	return output
}

func blockRecordFileFromAPI(input apiBlockRecordFile) (output core.BlockRecordFile) {
	output = core.BlockRecordFile{ID: input.ID, Hash: input.Hash, Type: input.Type, Format: input.Format, Size: input.Size}

	if input.Name != "" {
		output.TagsDecoded = append(output.TagsDecoded, core.FileTagName{Name: input.Name})
	}
	if input.Folder != "" {
		output.TagsDecoded = append(output.TagsDecoded, core.FileTagFolder{Name: input.Folder})
	}
	if input.Description != "" {
		output.TagsDecoded = append(output.TagsDecoded, core.FileTagDescription{Description: input.Description})
	}

	for _, tag := range input.Metadata {
		switch tag.Type {
		case core.TagTypeDateCreated:
			if dateF, err := time.Parse(dateFormat, tag.Value); err == nil {
				output.TagsDecoded = append(output.TagsDecoded, core.FileTagDateCreated{Date: dateF})
			}
		}
	}

	for n := range input.TagsRaw {
		if !isFileTagKnownMetadata(input.TagsRaw[n].Type) {
			output.TagsRaw = append(output.TagsRaw, core.BlockRecordFileTag{Type: input.TagsRaw[n].Type, Data: input.TagsRaw[n].Data})
		}
	}

	return output
}
