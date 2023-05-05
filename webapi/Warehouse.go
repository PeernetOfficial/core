/*
File Name:  Warehouse.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"github.com/google/uuid"
	"net/http"
	"strconv"

	"github.com/PeernetOfficial/core/warehouse"
)

// WarehouseResult is the response to creating a new file in the warehouse
type WarehouseResult struct {
	Status int    `json:"status"` // See warehouse.StatusX.
	Hash   []byte `json:"hash"`   // Hash of the file.
}

/*
ApiWarehouseCreateFile creates a file in the warehouse.

Request:    POST /warehouse/create with raw data to create as new file
Response:   200 with JSON structure WarehouseResult
*/
func (api *WebapiInstance) ApiWarehouseCreateFile(w http.ResponseWriter, r *http.Request) {
	// changing parameter to take ID as a parameter for upload and file itself
	ID := r.FormValue("id")
	file, handler, err := r.FormFile("File")
	if err != nil {
		api.Backend.LogError("warehouse.CreateFile", "error: %v", err)
		EncodeJSON(api.Backend, w, r, errorResponse{function: "warehouse.CreateFile", error: err.Error()})
		return
	}

	var hash []byte
	var status int

	// checks if there is a new upload and then
	if ID != "" {
		IDUUID, err := uuid.Parse(ID)
		if err != nil {
			api.Backend.LogError("warehouse.CreateFile", "error: %v", err)
			EncodeJSON(api.Backend, w, r, errorResponse{function: "warehouse.CreateFile", error: err.Error()})
			return
		}

		info := api.uploadLookup(IDUUID)
		if info == nil {
			var newInfo UploadStatus
			newInfo.ID = IDUUID
			newInfo.Progress.TotalSize = uint64(handler.Size)
			api.Backend.LogError("warehouse.CreateFile", "%v", newInfo)
			api.uploadAdd(&newInfo)
			hash, status, err = api.Backend.UserWarehouse.CreateFile(file, uint64(handler.Size), &newInfo)
		} else {
			info.Progress.TotalSize = uint64(handler.Size)
			api.Backend.LogError("warehouse.CreateFile", "%v", info)
			hash, status, err = api.Backend.UserWarehouse.CreateFile(file, uint64(handler.Size), info)
		}

		api.Backend.LogError("warehouse.CreateFile", "outside Create file: %v", info)

	} else {
		// File := r.
		hash, status, err = api.Backend.UserWarehouse.CreateFile(file, uint64(handler.Size), nil)
	}

	if err != nil {
		api.Backend.LogError("warehouse.CreateFile", "status %d error: %v", status, err)
		EncodeJSON(api.Backend, w, r, errorResponse{function: "warehouse.CreateFile", error: err.Error()})
		return
	}

	// Temporary log to check the output for warehouse API
	api.Backend.LogError("warehouse.CreateFile", "output %v", WarehouseResult{Status: status, Hash: hash})

	EncodeJSON(api.Backend, w, r, WarehouseResult{Status: status, Hash: hash})
}

/*
apiWarehouseCreateFilePath creates a file in the warehouse by copying it from an existing file.
Warning: An attacker could supply any local file using this function, put them into storage and read them! No input path verification or limitation is done.
In the future the API should be secured using a random API key and setting the CORS header prohibiting regular browsers to access the API.

Request:    GET /warehouse/create/path?path=[target path on disk]
Response:   200 with JSON structure WarehouseResult
*/
func (api *WebapiInstance) apiWarehouseCreateFilePath(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	filePath := r.Form.Get("path")
	if filePath == "" {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	hash, status, err := api.Backend.UserWarehouse.CreateFileFromPath(filePath)

	if err != nil {
		api.Backend.LogError("warehouse.CreateFile", "status %d error: %v", status, err)
	}

	EncodeJSON(api.Backend, w, r, WarehouseResult{Status: status, Hash: hash})
}

/*
apiWarehouseReadFile reads a file in the warehouse.

Request:    GET /warehouse/read?hash=[hash]

	Optional parameters &offset=[file offset]&limit=[read limit in bytes]

Response:   200 with the raw file data

	404 if file was not found
	500 in case of internal error opening the file
*/
func (api *WebapiInstance) apiWarehouseReadFile(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	hash, valid1 := DecodeBlake3Hash(r.Form.Get("hash"))
	if !valid1 {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	offset, _ := strconv.Atoi(r.Form.Get("offset"))
	limit, _ := strconv.Atoi(r.Form.Get("limit"))

	status, bytesRead, err := api.Backend.UserWarehouse.ReadFile(hash, int64(offset), int64(limit), w)

	switch status {
	case warehouse.StatusFileNotFound:
		w.WriteHeader(http.StatusNotFound)
		return
	case warehouse.StatusInvalidHash, warehouse.StatusErrorOpenFile, warehouse.StatusErrorSeekFile:
		w.WriteHeader(http.StatusInternalServerError)
		return
		// Cannot catch warehouse.StatusErrorReadFile since data may have been already returned.
		// In the future a special header indicating the expected file length could be sent (would require a callback in ReadFile), although the caller should already know the file size based on metadata.
	}

	if err != nil {
		api.Backend.LogError("warehouse.ReadFile", "status %d read %d error: %v", status, bytesRead, err)
	}
}

/*
apiWarehouseDeleteFile deletes a file in the warehouse.

Request:    GET /warehouse/delete?hash=[hash]
Response:   200 with JSON structure WarehouseResult
*/
func (api *WebapiInstance) apiWarehouseDeleteFile(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	hash, valid1 := DecodeBlake3Hash(r.Form.Get("hash"))
	if !valid1 {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	status, err := api.Backend.UserWarehouse.DeleteFile(hash)

	if err != nil {
		api.Backend.LogError("warehouse.DeleteFile", "status %d error: %v", status, err)
	}

	EncodeJSON(api.Backend, w, r, WarehouseResult{Status: status, Hash: hash})
}

/*
apiWarehouseReadFilePath reads a file from the warehouse and stores it to the target file. It fails with StatusErrorTargetExists if the target file already exists.
The path must include the full directory and file name.

Request:    GET /warehouse/read/path?hash=[hash]&path=[target path on disk]

	Optional parameters &offset=[file offset]&limit=[read limit in bytes]

Response:   200 with JSON structure WarehouseResult
*/
func (api *WebapiInstance) apiWarehouseReadFilePath(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	hash, valid1 := DecodeBlake3Hash(r.Form.Get("hash"))
	if !valid1 {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	targetFile := r.Form.Get("path")
	offset, _ := strconv.Atoi(r.Form.Get("offset"))
	limit, _ := strconv.Atoi(r.Form.Get("limit"))

	status, bytesRead, err := api.Backend.UserWarehouse.ReadFileToDisk(hash, int64(offset), int64(limit), targetFile)

	if err != nil {
		api.Backend.LogError("warehouse.ReadFileToDisk", "status %d read %d error: %v", status, bytesRead, err)
	}

	EncodeJSON(api.Backend, w, r, WarehouseResult{Status: status, Hash: hash})
}
