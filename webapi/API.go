/*
File Name:  API.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/PeernetOfficial/core"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

// Router can be used to register additional API functions
var Router *mux.Router

// WSUpgrader is used for websocket functionality. It allows all requests.
var WSUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// allow all connections by default
		return true
	},
}

// Start starts the API. ListenAddresses is a list of IP:Ports.
// The certificate file and key are only used if SSL is enabled. The read and write timeout may be 0 for no timeout.
func Start(ListenAddresses []string, UseSSL bool, CertificateFile, CertificateKey string, TimeoutRead, TimeoutWrite time.Duration) {
	if len(ListenAddresses) == 0 {
		return
	}

	Router = mux.NewRouter()

	Router.HandleFunc("/test", apiTest).Methods("GET")
	Router.HandleFunc("/status", apiStatus).Methods("GET")
	Router.HandleFunc("/account/info", apiAccountInfo).Methods("GET")
	Router.HandleFunc("/account/delete", apiAccountDelete).Methods("GET")
	Router.HandleFunc("/blockchain/header", apiBlockchainHeaderFunc).Methods("GET")
	Router.HandleFunc("/blockchain/append", apiBlockchainAppend).Methods("POST")
	Router.HandleFunc("/blockchain/read", apiBlockchainRead).Methods("GET")
	Router.HandleFunc("/blockchain/file/add", apiBlockchainFileAdd).Methods("POST")
	Router.HandleFunc("/blockchain/file/list", apiBlockchainFileList).Methods("GET")
	Router.HandleFunc("/blockchain/file/delete", apiBlockchainFileDelete).Methods("POST")
	Router.HandleFunc("/blockchain/file/update", apiBlockchainFileUpdate).Methods("POST")
	Router.HandleFunc("/profile/list", apiProfileList).Methods("GET")
	Router.HandleFunc("/profile/read", apiProfileRead).Methods("GET")
	Router.HandleFunc("/profile/write", apiProfileWrite).Methods("POST")
	Router.HandleFunc("/profile/delete", apiProfileDelete).Methods("POST")
	Router.HandleFunc("/search", apiSearch).Methods("POST")
	Router.HandleFunc("/search/result", apiSearchResult).Methods("GET")
	Router.HandleFunc("/search/result/ws", apiSearchResultStream).Methods("GET")
	Router.HandleFunc("/search/statistic", apiSearchStatistic).Methods("GET")
	Router.HandleFunc("/search/terminate", apiSearchTerminate).Methods("GET")
	Router.HandleFunc("/explore", apiExplore).Methods("GET")
	Router.HandleFunc("/file/format", apiFileFormat).Methods("GET")
	Router.HandleFunc("/download/start", apiDownloadStart).Methods("GET")
	Router.HandleFunc("/download/status", apiDownloadStatus).Methods("GET")
	Router.HandleFunc("/download/action", apiDownloadAction).Methods("GET")
	Router.HandleFunc("/warehouse/create", apiWarehouseCreateFile).Methods("POST")
	Router.HandleFunc("/warehouse/create/path", apiWarehouseCreateFilePath).Methods("GET")
	Router.HandleFunc("/warehouse/read", apiWarehouseReadFile).Methods("GET")
	Router.HandleFunc("/warehouse/delete", apiWarehouseDeleteFile).Methods("GET")
	Router.HandleFunc("/file/read", apiFileRead).Methods("GET")
	Router.HandleFunc("/file/view", apiFileView).Methods("GET")

	for _, listen := range ListenAddresses {
		go startWebAPI(listen, UseSSL, CertificateFile, CertificateKey, Router, "API", TimeoutRead, TimeoutWrite)
	}
}

// startWebAPI starts a web-server with given parameters and logs the status. If may block forever and only returns if there is an error.
// The certificate file and key are only used if SSL is enabled. The read and write timeout may be 0 for no timeout.
func startWebAPI(WebListen string, UseSSL bool, CertificateFile, CertificateKey string, Handler http.Handler, Info string, ReadTimeout, WriteTimeout time.Duration) {
	core.Filters.LogError("startWebAPI", "Start API at '%s'\n", WebListen)

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12} // for security reasons disable TLS 1.0/1.1

	server := &http.Server{
		Addr:         WebListen,
		Handler:      Handler,
		ReadTimeout:  ReadTimeout,  // ReadTimeout is the maximum duration for reading the entire request, including the body.
		WriteTimeout: WriteTimeout, // WriteTimeout is the maximum duration before timing out writes of the response. This includes processing time and is therefore the max time any HTTP function may take.
		//IdleTimeout:  IdleTimeout,  // IdleTimeout is the maximum amount of time to wait for the next request when keep-alives are enabled.
		TLSConfig: tlsConfig,
	}

	if UseSSL {
		// HTTPS
		if err := server.ListenAndServeTLS(CertificateFile, CertificateKey); err != nil {
			core.Filters.LogError("startWebAPI", "Error listening on '%s': %v\n", WebListen, err)
		}
	} else {
		// HTTP
		if err := server.ListenAndServe(); err != nil {
			core.Filters.LogError("startWebAPI", "Error listening on '%s': %v\n", WebListen, err)
		}
	}
}

// EncodeJSON encodes the data as JSON
func EncodeJSON(w http.ResponseWriter, r *http.Request, data interface{}) (err error) {
	w.Header().Set("Content-Type", "application/json")

	err = json.NewEncoder(w).Encode(data)
	if err != nil {
		core.Filters.LogError("EncodeJSON", "Error writing data for route '%s': %v\n", r.URL.Path, err)
	}

	return err
}

// DecodeJSON decodes input JSON data server side sent either via GET or POST. It does not limit the maximum amount to read.
// In case of error it will automatically send an error to the client.
func DecodeJSON(w http.ResponseWriter, r *http.Request, data interface{}) (err error) {
	if r.Body == nil {
		http.Error(w, "", http.StatusBadRequest)
		return errors.New("no data")
	}

	err = json.NewDecoder(r.Body).Decode(data)
	if err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return err
	}

	return nil
}
