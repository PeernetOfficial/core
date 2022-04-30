/*
File Name:  Download.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"bytes"
	"errors"
	"github.com/PeernetOfficial/core/blockchain"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type hostInfo struct {
	api      *WebapiInstance
	info     *downloadInfo
	filepath string
}

/*
apiCloneFile starts the download of a file, adds it the current warehouse and adds it
to your blockchain. The path is the full path on disk to store the file.
The hash parameter identifies the file to download. The node ID identifies the blockchain (i.e., the "owner" of the file).

The following is a prototype implementation

Request:    GET /follow/file?path=[target path on disk]&hash=[file hash to download]&node=[node ID]
Result:     200 with JSON structure apiResponseDownloadStatus
*/
func (api *WebapiInstance) apiHostFile(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	// validate hashes, must be blake3
	hash, valid1 := DecodeBlake3Hash(r.Form.Get("hash"))
	nodeID, valid2 := DecodeBlake3Hash(r.Form.Get("node"))
	if !valid1 || !valid2 {
		http.Error(w, "node ID or hash not valid", http.StatusBadRequest)
		return
	}

	filePath := r.Form.Get("path")
	if filePath == "" {
		http.Error(w, "file path provided is blank", http.StatusBadRequest)
		return
	}

	info := &downloadInfo{backend: api.backend, api: api, id: uuid.New(), created: time.Now(), hash: hash, nodeID: nodeID}

	// create the file immediately
	if info.initDiskFile(filePath) != nil {
		EncodeJSON(api.backend, w, r, apiResponseDownloadStatus{APIStatus: DownloadResponseFileInvalid})
		return
	}

	// add the download to the list
	api.downloadAdd(info)

	// Setting host information
	var HostInfo hostInfo
	HostInfo.api = api
	HostInfo.info = info
	HostInfo.filepath = filePath

	// start the download!
	go HostInfo.StartHostDownload()

	EncodeJSON(api.backend, w, r, apiResponseDownloadStatus{APIStatus: DownloadResponseSuccess, ID: info.id, DownloadStatus: DownloadWaitMetadata})
}

// StartHostDownload Starts the download, adds the downloaded file to the warehouse and then the following file is added to the user blockchain
func (info *hostInfo) StartHostDownload() error {
	// current user?
	if bytes.Equal(info.info.nodeID, info.info.backend.SelfNodeID()) {
		info.info.DownloadSelf()
		return nil
	}

	for n := 0; n < 3 && info.info.peer == nil; n++ {
		_, info.info.peer, _ = info.info.backend.FindNode(info.info.nodeID, time.Second*5)

		if info.info.status == DownloadCanceled {
			return errors.New("DownloadCanceled")
		}
	}

	if info.info.peer != nil {
		info.info.Download()

		// Adding the following file the warehouse
		if info.filepath == "" {
			return errors.New("File Path empty. ")
		}

		hash, status, err := info.api.backend.UserWarehouse.CreateFileFromPath(info.filepath)

		if err != nil {
			info.api.backend.LogError("warehouse.CreateFile", "status %d error: %v", status, err)
		}

		// Adding file to the blockchain
		var fileMetaData blockchain.BlockRecordFile

		// Add the following file to the blockchain
		fileMetaData.ID = uuid.New()
		fileMetaData.Hash = hash

		_, fileSize, _, _ := info.api.backend.UserWarehouse.FileExists(fileMetaData.Hash)
		// setting file size
		fileMetaData.Size = fileSize

		// create block based on file information provided
		block := blockRecordFileFromAPI(info.info.file)

		var filesAdd []blockchain.BlockRecordFile
		// appending the block to the file added
		filesAdd = append(filesAdd, block)

		info.api.backend.UserBlockchain.AddFiles(filesAdd)

	} else {
		info.info.status = DownloadCanceled
	}

	return nil
}
