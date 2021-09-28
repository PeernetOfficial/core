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

	EncodeJSON(w, r, apiBlockchainHeader{Version: version, Height: height, PeerID: hex.EncodeToString(publicKey.SerializeCompressed())})
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
	Status  int    `json:"status"`  // Status: 0 = Success, 1 = Error invalid data
	Height  uint64 `json:"height"`  // Height of the blockchain (number of blocks).
	Version uint64 `json:"version"` // Version of the blockchain.
}

/*
apiBlockchainSelfAppend appends a block to the blockchain. This is a low-level function for already encoded blocks.
Do not use this function. Adding invalid data to the blockchain may corrupt it which might result in blacklisting by other peers.

Request:    POST /blockchain/self/append with JSON structure apiBlockchainBlockRaw
Response:   200 with JSON structure apiBlockchainBlockStatus
*/
func apiBlockchainSelfAppend(w http.ResponseWriter, r *http.Request) {
	var input apiBlockchainBlockRaw
	if err := DecodeJSON(w, r, &input); err != nil {
		return
	}

	var records []core.BlockRecordRaw

	for _, record := range input.Records {
		records = append(records, core.BlockRecordRaw{Type: record.Type, Data: record.Data})
	}

	newHeight, newVersion, status := core.UserBlockchainAppend(records)

	EncodeJSON(w, r, apiBlockchainBlockStatus{Status: status, Height: newHeight, Version: newVersion})
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

// apiFileMetadata contains metadata information.
type apiFileMetadata struct {
	Type uint16 `json:"type"` // See core.TagX constants.
	Name string `json:"name"` // User friendly name of the metadata type. Use the Type fields to identify the metadata as this name may change.
	// Depending on the exact type, one of the below fields is used for proper encoding:
	Text string    `json:"text"` // Text value. UTF-8 encoding.
	Blob []byte    `json:"blob"` // Binary data
	Date time.Time `json:"date"` // Date
}

// apiBlockRecordFile is the metadata of a file published on the blockchain
type apiBlockRecordFile struct {
	ID          uuid.UUID         `json:"id"`          // Unique ID.
	Hash        []byte            `json:"hash"`        // Blake3 hash of the file data
	Type        uint8             `json:"type"`        // File Type. For example audio or document. See TypeX.
	Format      uint16            `json:"format"`      // File Format. This is more granular, for example PDF or Word file. See FormatX.
	Size        uint64            `json:"size"`        // Size of the file
	Folder      string            `json:"folder"`      // Folder, optional
	Name        string            `json:"name"`        // Name of the file
	Description string            `json:"description"` // Description. This is expected to be multiline and contain hashtags!
	Date        time.Time         `json:"date"`        // Date shared
	NodeID      []byte            `json:"nodeid"`      // Node ID, owner of the file
	Metadata    []apiFileMetadata `json:"metadata"`    // Additional metadata.
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

	EncodeJSON(w, r, result)
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
	if err := DecodeJSON(w, r, &input); err != nil {
		return
	}

	var filesAdd []core.BlockRecordFile

	for _, file := range input.Files {
		if file.ID == uuid.Nil { // if the ID is not provided by the caller, set it
			file.ID = uuid.New()
		}

		filesAdd = append(filesAdd, blockRecordFileFromAPI(file))
	}

	newHeight, newVersion, status := core.UserBlockchainAddFiles(filesAdd)

	EncodeJSON(w, r, apiBlockchainBlockStatus{Status: status, Height: newHeight, Version: newVersion})
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

	EncodeJSON(w, r, result)
}

/*
apiBlockchainSelfDeleteFile deletes files with the provided IDs. Other fields are ignored.

Request:    POST /blockchain/self/delete/file with JSON structure apiBlockAddFiles
Response:   200 with JSON structure apiBlockchainBlockStatus
*/
func apiBlockchainSelfDeleteFile(w http.ResponseWriter, r *http.Request) {
	var input apiBlockAddFiles
	if err := DecodeJSON(w, r, &input); err != nil {
		return
	}

	var deleteIDs []uuid.UUID

	for n := range input.Files {
		deleteIDs = append(deleteIDs, input.Files[n].ID)
	}

	newHeight, newVersion, status := core.UserBlockchainDeleteFiles(deleteIDs)

	EncodeJSON(w, r, apiBlockchainBlockStatus{Status: status, Height: newHeight, Version: newVersion})
}

// --- conversion from core to API data ---

func blockRecordFileToAPI(input core.BlockRecordFile) (output apiBlockRecordFile) {
	output = apiBlockRecordFile{ID: input.ID, Hash: input.Hash, NodeID: input.NodeID, Type: input.Type, Format: input.Format, Size: input.Size, Metadata: []apiFileMetadata{}}

	for _, tag := range input.Tags {
		switch tag.Type {
		case core.TagName:
			output.Name = tag.Text()

		case core.TagFolder:
			output.Folder = tag.Text()

		case core.TagDescription:
			output.Description = tag.Text()

		case core.TagDateShared:
			output.Date, _ = tag.Date()

		case core.TagDateCreated:
			date, _ := tag.Date()
			output.Metadata = append(output.Metadata, apiFileMetadata{Type: tag.Type, Name: "Date Created", Date: date})

		default:
			output.Metadata = append(output.Metadata, apiFileMetadata{Type: tag.Type, Blob: tag.Data})
		}
	}

	return output
}

func blockRecordFileFromAPI(input apiBlockRecordFile) (output core.BlockRecordFile) {
	output = core.BlockRecordFile{ID: input.ID, Hash: input.Hash, Type: input.Type, Format: input.Format, Size: input.Size}

	if input.Name != "" {
		output.Tags = append(output.Tags, core.TagFromText(core.TagName, input.Name))
	}
	if input.Folder != "" {
		output.Tags = append(output.Tags, core.TagFromText(core.TagFolder, input.Folder))
	}
	if input.Description != "" {
		output.Tags = append(output.Tags, core.TagFromText(core.TagDescription, input.Description))
	}

	for _, meta := range input.Metadata {
		switch meta.Type {
		case core.TagName, core.TagFolder, core.TagDescription, core.TagDateShared:

		case core.TagDateCreated:
			output.Tags = append(output.Tags, core.TagFromDate(meta.Type, meta.Date))

		default:
			output.Tags = append(output.Tags, core.BlockRecordFileTag{Type: meta.Type, Data: meta.Blob})
		}
	}

	return output
}
