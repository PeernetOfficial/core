/*
File Name:  Download.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import "net/http"

type apiResponseDownloadStatus struct {
	Status int `json:"status"` // Status: 0 = Success, 1 = Error download not found
	// TODO: Add progress size, total size, number of peers in swarm, etc.
}

/*
apiDownloadStart starts the download of a file. The path is the full path on disk to store the file.
The hash parameter identifies the file to download. The blockchain parameter is the node ID of the peer who shared the file on its blockchain (i.e., the "owner" of the file).

Request:    GET /download/start?path=[target path on disk]&hash=[file hash to download]&blockchain=[node ID]
Result:     200 with JSON structure apiResponseDownloadStatus
*/
func apiDownloadStart(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	filePath := r.Form.Get("path")
	hash := r.Form.Get("hash")
	blockchain := r.Form.Get("blockchain")
	if filePath == "" || hash == "" || blockchain == "" {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	// TODO

	EncodeJSON(w, r, apiResponseDownloadStatus{Status: 0})
}

/*
apiDownloadStatus returns the status of an active download. The hash and blockchain parameters must be the same as /download/start.

Request:    GET /download/status?hash=[file hash]&blockchain=[node ID]
Result:     200 with JSON structure apiResponseDownloadStatus
*/
func apiDownloadStatus(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	hash := r.Form.Get("hash")
	blockchain := r.Form.Get("blockchain")
	if hash == "" || blockchain == "" {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	// TODO

	EncodeJSON(w, r, apiResponseDownloadStatus{Status: 1})
}
