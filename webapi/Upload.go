package webapi

import (
	"github.com/google/uuid"
	"math"
	"net/http"
	"sync"
)

type UploadStatus struct {
	APIStatus    int       `json:"apistatus"` // Status of the API call. See DownloadResponseX.
	ID           uuid.UUID `json:"id"`        // Download ID. This can be used to query the latest status and take actions.
	sync.RWMutex           // Mutext for changing the status

	UploadStatus int `json:"uploadstatus"` // Status of the download. See DownloadX.
	Progress     struct {
		TotalSize    uint64  `json:"totalsize"`    // Total size in bytes.
		UploadedSize uint64  `json:"uploadedsize"` // Count of bytes download so far.
		Percentage   float64 `json:"percentage"`   // Percentage downloaded. Rounded to 2 decimal points. Between 0.00 and 100.00.
	} `json:"progress"` // Progress of the download. Only valid for status >= DownloadWaitSwarm.
}

func (api *WebapiInstance) uploadAdd(info *UploadStatus) {
	api.uploadsMutex.Lock()
	api.uploads[info.ID] = info
	api.uploadsMutex.Unlock()
}

func (api *WebapiInstance) uploadDelete(id uuid.UUID) {
	api.uploadsMutex.Lock()
	delete(api.uploads, id)
	api.uploadsMutex.Unlock()
}

func (api *WebapiInstance) uploadLookup(id uuid.UUID) (info *UploadStatus) {
	api.uploadsMutex.Lock()
	info = api.uploads[id]
	api.uploadsMutex.Unlock()
	return info
}

// UploadID API to set a Status ID to track the upload
func (api *WebapiInstance) apiUploadID(w http.ResponseWriter, r *http.Request) {
	var info UploadStatus
	info.ID = uuid.New()

	// Create a upload ID for adding the upload
	// metadata later one
	api.uploadAdd(&info)

	EncodeJSON(api.Backend, w, r, info)
}

// Write is used to satisfy the io.Writer interface.
// Instead of writing somewhere, it simply aggregates
// the total bytes on each read
func (uploadStatus *UploadStatus) Write(p []byte) (n int, err error) {
	n = len(p)
	uploadStatus.Progress.UploadedSize += uint64(n)
	uploadStatus.Progress.Percentage = math.Round(float64(uploadStatus.Progress.UploadedSize)/float64(uploadStatus.Progress.TotalSize)*100*100) / 100
	return
}

// Get information about upload file status
func (api *WebapiInstance) apiUploadInfo(w http.ResponseWriter, r *http.Request) {
	ID := r.URL.Query().Get("id")
	if ID == "" {
		api.Backend.LogError("upload.UploadInformation", "error: %v", "ID parameter not passed")
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	IDUUID, err := uuid.Parse(ID)
	if err != nil {
		api.Backend.LogError("upload.UploadInformation", "error: %v", err)
		return
	}

	info := api.uploadLookup(IDUUID)
	if info == nil {
		EncodeJSON(api.Backend, w, r, UploadStatus{APIStatus: DownloadResponseIDNotFound})
		return
	}

	EncodeJSON(api.Backend, w, r, info)
}
