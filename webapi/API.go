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
	"log"
	"net/http"
	"time"

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

// Start starts the API. ListenAddresses is a list of IP:Ports
func Start(ListenAddresses []string, UseSSL bool, CertificateFile, CertificateKey string, TimeoutRead, TimeoutWrite time.Duration) {
	if len(ListenAddresses) == 0 {
		return
	}

	Router = mux.NewRouter()

	Router.HandleFunc("/test", apiTest).Methods("GET")
	Router.HandleFunc("/status", apiStatus).Methods("GET")
	Router.HandleFunc("/peer/self", apiPeerSelf).Methods("GET")
	Router.HandleFunc("/blockchain/self/header", apiBlockchainSelfHeader).Methods("GET")
	Router.HandleFunc("/blockchain/self/append", apiBlockchainSelfAppend).Methods("POST")
	Router.HandleFunc("/blockchain/self/read", apiBlockchainSelfRead).Methods("GET")
	Router.HandleFunc("/blockchain/self/add/file", apiBlockchainSelfAddFile).Methods("POST")
	Router.HandleFunc("/blockchain/self/list/file", apiBlockchainSelfListFile).Methods("GET")
	Router.HandleFunc("/blockchain/self/delete/file", apiBlockchainSelfDeleteFile).Methods("POST")
	Router.HandleFunc("/profile/list", apiProfileList).Methods("GET")
	Router.HandleFunc("/profile/read", apiProfileRead).Methods("GET")
	Router.HandleFunc("/profile/write", apiProfileWrite).Methods("POST")
	Router.HandleFunc("/profile/delete", apiProfileDelete).Methods("POST")
	Router.HandleFunc("/search", apiSearch).Methods("POST")
	Router.HandleFunc("/search/result", apiSearchResult).Methods("GET")
	//Router.HandleFunc("/search/result/ws", apiSearchResultStream).Methods("GET")
	Router.HandleFunc("/search/statistic", apiSearchStatistic).Methods("GET")
	Router.HandleFunc("/search/terminate", apiSearchTerminate).Methods("GET")
	Router.HandleFunc("/explore", apiExplore).Methods("GET")
	Router.HandleFunc("/file/format", apiFileFormat).Methods("GET")
	Router.HandleFunc("/download/start", apiDownloadStart).Methods("GET")
	Router.HandleFunc("/download/status", apiDownloadStatus).Methods("GET")
	Router.HandleFunc("/download/action", apiDownloadAction).Methods("GET")

	for _, listen := range ListenAddresses {
		go startWebServer(listen, UseSSL, CertificateFile, CertificateKey, Router, "API", TimeoutRead, TimeoutWrite)
	}
}

// startWebServer starts a web-server with given parameters and logs the status. If may block forever and only returns if there is an error.
func startWebServer(WebListen string, UseSSL bool, CertificateFile, CertificateKey string, Handler http.Handler, Info string, ReadTimeout, WriteTimeout time.Duration) {
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
			log.Printf("Error listening on '%s': %v\n", WebListen, err)
		}
	} else {
		// HTTP
		if err := server.ListenAndServe(); err != nil {
			log.Printf("Error listening on '%s': %v\n", WebListen, err)
		}
	}
}

// EncodeJSON encodes the data as JSON
func EncodeJSON(w http.ResponseWriter, r *http.Request, data interface{}) (err error) {
	w.Header().Set("Content-Type", "application/json")

	err = json.NewEncoder(w).Encode(data)
	if err != nil {
		log.Printf("Error writing data for route '%s': %v\n", r.URL.Path, err)
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
