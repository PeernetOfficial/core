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
	"github.com/PeernetOfficial/core/blockchain"
	"github.com/PeernetOfficial/core/merkle"
	"github.com/PeernetOfficial/core/protocol"
	"github.com/PeernetOfficial/core/warehouse"
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
	NodeID      []byte            `json:"nodeid"`      // Node ID, owner of the file. Read only.
	Metadata    []apiFileMetadata `json:"metadata"`    // Additional metadata.
}

// --- conversion from core to API data ---

func blockRecordFileToAPI(input blockchain.BlockRecordFile) (output apiFile) {
	output = apiFile{ID: input.ID, Hash: input.Hash, NodeID: input.NodeID, Type: input.Type, Format: input.Format, Size: input.Size, Metadata: []apiFileMetadata{}}

	for _, tag := range input.Tags {
		switch tag.Type {
		case blockchain.TagName:
			output.Name = tag.Text()

		case blockchain.TagFolder:
			output.Folder = tag.Text()

		case blockchain.TagDescription:
			output.Description = tag.Text()

		case blockchain.TagDateShared:
			output.Date, _ = tag.Date()

		case blockchain.TagDateCreated:
			date, _ := tag.Date()
			output.Metadata = append(output.Metadata, apiFileMetadata{Type: tag.Type, Name: "Date Created", Date: date})

		case blockchain.TagSharedByCount:
			output.Metadata = append(output.Metadata, apiFileMetadata{Type: tag.Type, Name: "Shared By Count", Number: tag.Number()})

		case blockchain.TagSharedByGeoIP:
			output.Metadata = append(output.Metadata, apiFileMetadata{Type: tag.Type, Name: "Shared By GeoIP", Text: tag.Text()})

		default:
			output.Metadata = append(output.Metadata, apiFileMetadata{Type: tag.Type, Blob: tag.Data})
		}
	}

	return output
}

func blockRecordFileFromAPI(input apiFile) (output blockchain.BlockRecordFile) {
	output = blockchain.BlockRecordFile{ID: input.ID, Hash: input.Hash, Type: input.Type, Format: input.Format, Size: input.Size}

	if input.Name != "" {
		output.Tags = append(output.Tags, blockchain.TagFromText(blockchain.TagName, input.Name))
	}
	if input.Folder != "" {
		output.Tags = append(output.Tags, blockchain.TagFromText(blockchain.TagFolder, input.Folder))
	}
	if input.Description != "" {
		output.Tags = append(output.Tags, blockchain.TagFromText(blockchain.TagDescription, input.Description))
	}

	for _, meta := range input.Metadata {
		if blockchain.IsTagVirtual(meta.Type) { // Virtual tags are not mapped back. They are read-only.
			continue
		}

		switch meta.Type {
		case blockchain.TagName, blockchain.TagFolder, blockchain.TagDescription: // auto mapped tags

		case blockchain.TagDateCreated:
			output.Tags = append(output.Tags, blockchain.TagFromDate(meta.Type, meta.Date))

		default:
			output.Tags = append(output.Tags, blockchain.BlockRecordFileTag{Type: meta.Type, Data: meta.Blob})
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
apiBlockchainFileAdd adds a file with the provided information to the blockchain.
Each file must be already stored in the Warehouse (virtual folders are exempt).
If any file is not stored in the Warehouse, the function aborts with the status code StatusNotInWarehouse.
If the block record encoding fails for any file, this function aborts with the status code StatusCorruptBlockRecord.
In case the function aborts, the blockchain remains unchanged.

Request:    POST /blockchain/file/add with JSON structure apiBlockAddFiles
Response:   200 with JSON structure apiBlockchainBlockStatus
			400 if invalid input
*/
func (api *WebapiInstance) apiBlockchainFileAdd(w http.ResponseWriter, r *http.Request) {
	var input apiBlockAddFiles
	if err := DecodeJSON(w, r, &input); err != nil {
		return
	}

	var filesAdd []blockchain.BlockRecordFile

	for _, file := range input.Files {
		if len(file.Hash) != protocol.HashSize {
			http.Error(w, "", http.StatusBadRequest)
			return
		}
		if file.ID == uuid.Nil { // if the ID is not provided by the caller, set it
			file.ID = uuid.New()
		}

		// Verify that the file exists in the warehouse. Folders are exempt from this check as they are only virtual.
		if !file.IsVirtualFolder() {
			if _, err := warehouse.ValidateHash(file.Hash); err != nil {
				http.Error(w, "", http.StatusBadRequest)
				return
			} else if _, fileInfo, status, _ := api.backend.UserWarehouse.FileExists(file.Hash); status != warehouse.StatusOK {
				EncodeJSON(api.backend, w, r, apiBlockchainBlockStatus{Status: blockchain.StatusNotInWarehouse})
				return
			} else {
				file.Size = uint64(fileInfo.Size())
			}
		} else {
			file.Hash = protocol.HashData(nil)
			file.Size = 0
		}

		blockRecord := blockRecordFileFromAPI(file)

		// Set the merkle tree info as appropriate.
		if !setFileMerkleInfo(api.backend, &blockRecord) {
			EncodeJSON(api.backend, w, r, apiBlockchainBlockStatus{Status: blockchain.StatusNotInWarehouse})
			return
		}

		filesAdd = append(filesAdd, blockRecord)
	}

	newHeight, newVersion, status := api.backend.UserBlockchain.AddFiles(filesAdd)

	EncodeJSON(api.backend, w, r, apiBlockchainBlockStatus{Status: status, Height: newHeight, Version: newVersion})
}

/*
apiBlockchainFileList lists all files stored on the blockchain.

Request:    GET /blockchain/file/list
Response:   200 with JSON structure apiBlockAddFiles
*/
func (api *WebapiInstance) apiBlockchainFileList(w http.ResponseWriter, r *http.Request) {
	files, status := api.backend.UserBlockchain.ListFiles()

	var result apiBlockAddFiles

	for _, file := range files {
		result.Files = append(result.Files, blockRecordFileToAPI(file))
	}

	result.Status = status

	EncodeJSON(api.backend, w, r, result)
}

/*
apiBlockchainFileDelete deletes files with the provided IDs. Other fields are ignored.
It will automatically delete the file in the Warehouse if there are no other references.

Request:    POST /blockchain/file/delete with JSON structure apiBlockAddFiles
Response:   200 with JSON structure apiBlockchainBlockStatus
*/
func (api *WebapiInstance) apiBlockchainFileDelete(w http.ResponseWriter, r *http.Request) {
	var input apiBlockAddFiles
	if err := DecodeJSON(w, r, &input); err != nil {
		return
	}

	var deleteIDs []uuid.UUID

	for n := range input.Files {
		deleteIDs = append(deleteIDs, input.Files[n].ID)
	}

	newHeight, newVersion, deletedFiles, status := api.backend.UserBlockchain.DeleteFiles(deleteIDs)

	// If successfully deleted from the blockchain, delete from the Warehouse in case there are no other references.
	if status == blockchain.StatusOK {
		for n := range deletedFiles {
			if files, status := api.backend.UserBlockchain.FileExists(deletedFiles[n].Hash); status == blockchain.StatusOK && len(files) == 0 {
				api.backend.UserWarehouse.DeleteFile(deletedFiles[n].Hash)
			}
		}
	}

	EncodeJSON(api.backend, w, r, apiBlockchainBlockStatus{Status: status, Height: newHeight, Version: newVersion})
}

/*
apiBlockchainSelfUpdateFile updates files that are already published on the blockchain.

Request:    POST /blockchain/file/update with JSON structure apiBlockAddFiles
Response:   200 with JSON structure apiBlockchainBlockStatus
			400 if invalid input
*/
func (api *WebapiInstance) apiBlockchainFileUpdate(w http.ResponseWriter, r *http.Request) {
	var input apiBlockAddFiles
	if err := DecodeJSON(w, r, &input); err != nil {
		return
	}

	var filesAdd []blockchain.BlockRecordFile

	for _, file := range input.Files {
		if len(file.Hash) != protocol.HashSize {
			http.Error(w, "", http.StatusBadRequest)
			return
		} else if file.ID == uuid.Nil { // if the ID is not provided by the caller, abort
			http.Error(w, "", http.StatusBadRequest)
			return
		}

		// Verify that the file exists in the warehouse. Folders are exempt from this check as they are only virtual.
		if !file.IsVirtualFolder() {
			if _, err := warehouse.ValidateHash(file.Hash); err != nil {
				http.Error(w, "", http.StatusBadRequest)
				return
			} else if _, fileInfo, status, _ := api.backend.UserWarehouse.FileExists(file.Hash); status != warehouse.StatusOK {
				EncodeJSON(api.backend, w, r, apiBlockchainBlockStatus{Status: blockchain.StatusNotInWarehouse})
				return
			} else {
				file.Size = uint64(fileInfo.Size())
			}
		} else {
			file.Hash = protocol.HashData(nil)
			file.Size = 0
		}

		blockRecord := blockRecordFileFromAPI(file)

		// Set the merkle tree info as appropriate.
		if !setFileMerkleInfo(api.backend, &blockRecord) {
			EncodeJSON(api.backend, w, r, apiBlockchainBlockStatus{Status: blockchain.StatusNotInWarehouse})
			return
		}

		filesAdd = append(filesAdd, blockRecord)
	}

	newHeight, newVersion, status := api.backend.UserBlockchain.ReplaceFiles(filesAdd)

	EncodeJSON(api.backend, w, r, apiBlockchainBlockStatus{Status: status, Height: newHeight, Version: newVersion})
}

// ---- metadata functions ----

// GetMetadata returns the specified metadata or nil if not available.
func (file *apiFile) GetMetadata(Type uint16) (info *apiFileMetadata) {
	for n := range file.Metadata {
		if file.Metadata[n].Type == Type {
			return &file.Metadata[n]
		}
	}

	return nil
}

// GetNumber returns the data as number. 0 if not available.
func (info *apiFileMetadata) GetNumber() uint64 {
	if info == nil {
		return 0
	}

	return info.Number
}

// IsVirtualFolder returns true if the file is a virtual folder
func (file *apiFile) IsVirtualFolder() bool {
	return file.Type == core.TypeFolder && file.Format == core.FormatFolder
}

// setFileMerkleInfo sets the merkle fields in the BlockRecordFile
func setFileMerkleInfo(backend *core.Backend, file *blockchain.BlockRecordFile) (valid bool) {
	if file.Size <= merkle.MinimumFragmentSize {
		// If smaller or equal than the minimum fragment size, the merkle tree is not used.
		file.MerkleRootHash = file.Hash
		file.FragmentSize = merkle.MinimumFragmentSize
	} else {
		// Get the information from the Warehouse .merkle companion file.
		tree, status, _ := backend.UserWarehouse.ReadMerkleTree(file.Hash, true)
		if status != warehouse.StatusOK {
			return false
		}

		file.MerkleRootHash = tree.RootHash
		file.FragmentSize = tree.FragmentSize
	}

	return true
}
