/*
File Name:  Download.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"encoding/hex"
	"math"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/PeernetOfficial/core"
	"github.com/google/uuid"
)

type apiResponseDownloadStatus struct {
	APIStatus      int                `json:"apistatus"`      // Status of the API call. See DownloadResponseX.
	ID             uuid.UUID          `json:"id"`             // Download ID. This can be used to query the latest status and take actions.
	DownloadStatus int                `json:"downloadstatus"` // Status of the download. See DownloadX.
	File           apiBlockRecordFile `json:"file"`           // File information. Only available for status >= DownloadWaitSwarm.
	Progress       struct {
		TotalSize      uint64  `json:"totalsize"`      // Total size in bytes.
		DownloadedSize uint64  `json:"downloadedsize"` // Count of bytes download so far.
		Percentage     float64 `json:"percentage"`     // Percentage downloaded. Rounded to 2 decimal points. Between 0.00 and 100.00.
	} `json:"progress"` // Progress of the download. Only valid for status >= DownloadWaitSwarm.
	Swarm struct {
		CountPeers uint64 `json:"countpeers"` // Count of peers participating in the swarm.
	} `json:"swarm"` // Information about the swarm. Only valid for status >= DownloadActive.
}

const (
	DownloadResponseSuccess       = 0 // Success
	DownloadResponseIDNotFound    = 1 // Error: Download ID not found.
	DownloadResponseFileInvalid   = 2 // Error: Target file cannot be used. For example, permissions denied to create it.
	DownloadResponseActionInvalid = 4 // Error: Invalid action. Pausing a non-active download, resuming a non-paused download, or canceling already canceled or finished.
	DownloadResponseFileWrite     = 5 // Error writing file.
)

// Download status list
const (
	DownloadWaitMetadata = 0 // Wait for file metadata.
	DownloadWaitSwarm    = 1 // Wait to join swarm.
	DownloadActive       = 2 // Active downloading. This only means it joined a swarm. It could still be stuck at any percentage (including 0%) if no seeders are available.
	DownloadPause        = 3 // Paused by the user.
	DownloadCanceled     = 4 // Canceled by the user before the download finished. Once canceled, a new download has to be started if the file shall be downloaded.
	DownloadFinished     = 5 // Download finished 100%.
)

/*
apiDownloadStart starts the download of a file. The path is the full path on disk to store the file.
The hash parameter identifies the file to download. The node ID identifies the blockchain (i.e., the "owner" of the file).

Request:    GET /download/start?path=[target path on disk]&hash=[file hash to download]&node=[node ID]
Result:     200 with JSON structure apiResponseDownloadStatus
*/
func apiDownloadStart(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	// validate hashes, must be blake3
	hash, valid1 := DecodeBlake3Hash(r.Form.Get("hash"))
	nodeID, valid2 := DecodeBlake3Hash(r.Form.Get("node"))
	if !valid1 || !valid2 {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	filePath := r.Form.Get("path")
	if filePath == "" {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	info := &downloadInfo{id: uuid.New(), created: time.Now(), hash: hash, nodeID: nodeID}

	// create the file immediately
	if info.initDiskFile(filePath) != nil {
		EncodeJSON(w, r, apiResponseDownloadStatus{APIStatus: DownloadResponseFileInvalid})
		return
	}

	// add the download to the list
	downloadAdd(info)

	// start the download!
	go info.Start()

	EncodeJSON(w, r, apiResponseDownloadStatus{APIStatus: DownloadResponseSuccess, ID: info.id, DownloadStatus: DownloadWaitMetadata})
}

/*
apiDownloadStatus returns the status of an active download.

Request:    GET /download/status?id=[download ID]
Result:     200 with JSON structure apiResponseDownloadStatus
*/
func apiDownloadStatus(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id, err := uuid.Parse(r.Form.Get("id"))
	if err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	info := downloadLookup(id)
	if info == nil {
		EncodeJSON(w, r, apiResponseDownloadStatus{APIStatus: DownloadResponseIDNotFound})
		return
	}

	info.RLock()

	response := apiResponseDownloadStatus{APIStatus: DownloadResponseSuccess, ID: info.id, DownloadStatus: info.status}

	if info.status >= DownloadWaitSwarm {
		response.File = info.fileU

		response.Progress.TotalSize = info.file.Size
		response.Progress.DownloadedSize = info.DiskFile.StoredSize

		response.Progress.Percentage = math.Round(float64(info.DiskFile.StoredSize)/float64(info.file.Size)*100*100) / 100
	}

	if info.status >= DownloadActive {
		response.Swarm.CountPeers = info.Swarm.CountPeers
	}

	info.RUnlock()

	EncodeJSON(w, r, response)
}

/*
apiDownloadAction pauses, resumes, and cancels a download. Once canceled, a new download has to be started if the file shall be downloaded.
Only active downloads can be paused. While a download is in discovery phase (querying metadata, joining swarm), it can only be canceled.
Action: 0 = Pause, 1 = Resume, 2 = Cancel.

Request:    GET /download/action?id=[download ID]&action=[action]
Result:     200 with JSON structure apiResponseDownloadStatus (using APIStatus and DownloadStatus)
*/
func apiDownloadAction(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id, err := uuid.Parse(r.Form.Get("id"))
	action, err2 := strconv.Atoi(r.Form.Get("action"))
	if err != nil || err2 != nil || action < 0 || action > 2 {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	info := downloadLookup(id)
	if info == nil {
		EncodeJSON(w, r, apiResponseDownloadStatus{APIStatus: DownloadResponseIDNotFound})
		return
	}

	apiStatus := 0

	switch action {
	case 0: // Pause
		apiStatus = info.Pause()

	case 1: // Resume
		apiStatus = info.Resume()

	case 2: // Cancel
		apiStatus = info.Cancel()
	}

	EncodeJSON(w, r, apiResponseDownloadStatus{APIStatus: apiStatus, ID: info.id, DownloadStatus: info.status})
}

// ---- download tracking ----

type downloadInfo struct {
	id           uuid.UUID // Download ID
	status       int       // Current status. See DownloadX.
	sync.RWMutex           // Mutext for changing the status

	// input
	hash   []byte // File hash
	nodeID []byte // Node ID of the owner

	// runtime data
	created time.Time // When the download was created.
	ended   time.Time // When the download was finished (only status = DownloadFinished).

	file  core.BlockRecordFile // File metadata (only status >= DownloadWaitSwarm)
	fileU apiBlockRecordFile   // Same as file metadata, but encoded for API

	DiskFile struct { // Target file on disk to store downloaded data
		Name       string   // File name
		Handle     *os.File // Target file (on disk) to store downloaded data
		StoredSize uint64   // Count of bytes downloaded and stored in the file
	}

	Swarm struct { // Information about the swarm. Only valid for status >= DownloadActive.
		CountPeers uint64 // Count of peers participating in the swarm.
	}
}

var (
	downloads      = make(map[uuid.UUID]*downloadInfo)
	downloadsMutex sync.RWMutex
)

func downloadAdd(info *downloadInfo) {
	downloadsMutex.Lock()
	downloads[info.id] = info
	downloadsMutex.Unlock()
}

func downloadDelete(id uuid.UUID) {
	downloadsMutex.Lock()
	delete(downloads, id)
	downloadsMutex.Unlock()
}

func downloadLookup(id uuid.UUID) (info *downloadInfo) {
	downloadsMutex.Lock()
	info = downloads[id]
	downloadsMutex.Unlock()
	return info
}

// DeleteDefer deletes the download from the downloads list after the given duration.
// It does not wait for the download to be finished.
func (info *downloadInfo) DeleteDefer(Duration time.Duration) {
	go func() {
		<-time.After(Duration)
		downloadDelete(info.id)
	}()
}

// DecodeBlake3Hash decodes a blake3 hash that is hex encoded
func DecodeBlake3Hash(text string) (hash []byte, valid bool) {
	hash, err := hex.DecodeString(text)
	return hash, err == nil && len(hash) == 256/8
}
