/*
File Name:  Warehouse.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"net/http"
	"strconv"

	"github.com/PeernetOfficial/core"
	"github.com/PeernetOfficial/core/warehouse"
)

// WarehouseResult is the response to creating a new file in the warehouse
type WarehouseResult struct {
	Status int    `json:"status"` // See warehouse.StatusX.
	Hash   []byte `json:"hash"`   // Hash of the file.
}

/*
apiWarehouseCreateFile creates a file in the warehouse.

Request:    POST /warehouse/create with raw data to create as new file
Response:   200 with JSON structure WarehouseResult
*/
func apiWarehouseCreateFile(w http.ResponseWriter, r *http.Request) {
	hash, status, err := core.UserWarehouse.CreateFile(r.Body)

	if err != nil {
		core.Filters.LogError("warehouese.CreateFile", "status %d error: %v", status, err)
	}

	EncodeJSON(w, r, WarehouseResult{Status: status, Hash: hash})
}

/*
apiWarehouseCreateFilePath creates a file in the warehouse by copying it from an existing file.
Warning: An attacker could supply any local file using this function, put them into storage and read them! No input path verification or limitation is done.
In the future the API should be secured using a random API key and setting the CORS header prohibiting regular browsers to access the API.

Request:    GET /warehouse/create/path?path=[target path on disk]
Response:   200 with JSON structure WarehouseResult
*/
func apiWarehouseCreateFilePath(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	filePath := r.Form.Get("path")
	if filePath == "" {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	hash, status, err := core.UserWarehouse.CreateFileFromPath(filePath)

	if err != nil {
		core.Filters.LogError("warehouese.CreateFile", "status %d error: %v", status, err)
	}

	EncodeJSON(w, r, WarehouseResult{Status: status, Hash: hash})
}

/*
apiWarehouseReadFile reads a file in the warehouse.

Request:    GET /warehouse/read?hash=[hash]
            Optional parameters &offset=[file offset]&limit=[read limit in bytes]
Response:   200 with the raw file data
			404 if file was not found
			500 in case of internal error opening the file
*/
func apiWarehouseReadFile(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	hash, valid1 := DecodeBlake3Hash(r.Form.Get("hash"))
	if !valid1 {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	offset, _ := strconv.Atoi(r.Form.Get("offset"))
	limit, _ := strconv.Atoi(r.Form.Get("limit"))

	status, err := core.UserWarehouse.ReadFile(hash, int64(offset), int64(limit), w)

	switch status {
	case warehouse.StatusFileNotFound:
		w.WriteHeader(http.StatusNotFound)
		return
	case warehouse.StatusInvalidHash, warehouse.StatusErrorOpenFile, warehouse.StatusErrorSeekFile:
		w.WriteHeader(http.StatusInternalServerError)
		return
		// Cannot catch WarehouseStatusErrorReadFile since data may have been already returned.
		// In the future a special header indicating the expected file length could be sent (would require a callback in ReadFile), although the caller should already know the file size based on metadata.
	}

	if err != nil {
		core.Filters.LogError("warehouese.ReadFile", "status %d error: %v", status, err)
	}
}

/*
apiWarehouseDeleteFile deletes a file in the warehouse.

Request:    GET /warehouse/delete?hash=[hash]
Response:   200 with JSON structure WarehouseResult
*/
func apiWarehouseDeleteFile(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	hash, valid1 := DecodeBlake3Hash(r.Form.Get("hash"))
	if !valid1 {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	status, err := core.UserWarehouse.DeleteFile(hash)

	if err != nil {
		core.Filters.LogError("warehouese.DeleteFile", "status %d error: %v", status, err)
	}

	EncodeJSON(w, r, WarehouseResult{Status: status, Hash: hash})
}
