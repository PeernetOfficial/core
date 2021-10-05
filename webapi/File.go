/*
File Name:  File.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"net/http"
	"time"

	"github.com/PeernetOfficial/core"
	"github.com/google/uuid"
)

// apiFileMetadata contains metadata information.
type apiFileMetadata struct {
	Type uint16 `json:"type"` // See core.TagX constants.
	Name string `json:"name"` // User friendly name of the metadata type. Use the Type fields to identify the metadata as this name may change.
	// Depending on the exact type, one of the below fields is used for proper encoding:
	Text   string    `json:"text"`   // Text value. UTF-8 encoding.
	Blob   []byte    `json:"blob"`   // Binary data
	Date   time.Time `json:"date"`   // Date
	Number uint64    `json:"number"` // Number
}

// apiFile is the metadata of a file published on the blockchain
type apiFile struct {
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

// --- conversion from core to API data ---

func blockRecordFileToAPI(input core.BlockRecordFile) (output apiFile) {
	output = apiFile{ID: input.ID, Hash: input.Hash, NodeID: input.NodeID, Type: input.Type, Format: input.Format, Size: input.Size, Metadata: []apiFileMetadata{}}

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

		case core.TagSharedByCount:
			output.Metadata = append(output.Metadata, apiFileMetadata{Type: tag.Type, Name: "Shared By Count", Number: tag.Number()})

		case core.TagSharedByGeoIP:
			output.Metadata = append(output.Metadata, apiFileMetadata{Type: tag.Type, Name: "Shared By GeoIP", Text: tag.Text()})

		default:
			output.Metadata = append(output.Metadata, apiFileMetadata{Type: tag.Type, Blob: tag.Data})
		}
	}

	return output
}

func blockRecordFileFromAPI(input apiFile) (output core.BlockRecordFile) {
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
		if core.IsTagVirtual(meta.Type) { // Virtual tags are not mapped back. They are read-only.
			continue
		}

		switch meta.Type {
		case core.TagName, core.TagFolder, core.TagDescription: // auto mapped tags

		case core.TagDateCreated:
			output.Tags = append(output.Tags, core.TagFromDate(meta.Type, meta.Date))

		default:
			output.Tags = append(output.Tags, core.BlockRecordFileTag{Type: meta.Type, Data: meta.Blob})
		}
	}

	return output
}

// --- File API ---

// apiBlockAddFiles contains a list of files from the blockchain
type apiBlockAddFiles struct {
	Files  []apiFile `json:"files"`  // List of files
	Status int       `json:"status"` // Status of the operation, only used when this structure is returned from the API.
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
