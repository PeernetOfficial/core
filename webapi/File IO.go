/*
File Name:  File IO.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/PeernetOfficial/core"
	"github.com/PeernetOfficial/core/btcec"
	"github.com/PeernetOfficial/core/protocol"
	"github.com/PeernetOfficial/core/warehouse"
)

/*
apiFileRead reads a file immediately from a remote peer. Use the /download functions to download a file.
This endpoint supports the Range, Content-Range and Content-Length headers. Multipart ranges are not supported and result in HTTP 400.
Instead of providing the node ID, the peer ID is also accepted in the &node= parameter.
The default timeout for connecting to the peer is 10 seconds.

Request:    GET /file/read?hash=[hash]&node=[node ID]
            Optional: &offset=[offset]&limit=[limit] or via Range header.
            Optional: &timeout=[seconds]
Response:   200 with the content
            206 with partial content
            400 if the parameters are invalid
            404 if the file was not found or other error on transfer initiate
            502 if unable to find or connect to the remote peer in time
*/
func apiFileRead(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	var err error

	// validate hashes (must be blake3) and other input
	fileHash, valid1 := DecodeBlake3Hash(r.Form.Get("hash"))
	nodeID, valid2 := DecodeBlake3Hash(r.Form.Get("node"))
	publicKey, err3 := core.PublicKeyFromPeerID(r.Form.Get("node"))
	if !valid1 || (!valid2 && err3 != nil) {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	timeoutSeconds, _ := strconv.Atoi(r.Form.Get("timeout"))
	if timeoutSeconds == 0 {
		timeoutSeconds = 10
	}
	timeout := time.Duration(timeoutSeconds) * time.Second

	offset, _ := strconv.Atoi(r.Form.Get("offset"))
	limit, _ := strconv.Atoi(r.Form.Get("limit"))

	// Range header?
	var ranges []HTTPRange
	if ranges, err = ParseRangeHeader(r.Header.Get("Range"), -1, true); err != nil || len(ranges) > 1 {
		http.Error(w, "", http.StatusBadRequest)
		return
	} else if len(ranges) == 1 {
		if ranges[0].length != -1 { // if length is not specified, limit remains 0 which is maximum
			limit = ranges[0].length
		}
		offset = ranges[0].start
	}

	// Is the file available in the local warehouse? In that case requesting it from the remote is unnecessary.
	if serveFileFromWarehouse(w, fileHash, uint64(offset), uint64(limit), ranges) {
		return
	}

	// try connecting via node ID or peer ID?
	var peer *core.PeerInfo

	if valid2 {
		peer, err = PeerConnectNode(nodeID, timeout)
	} else if err3 == nil {
		peer, err = PeerConnectPublicKey(publicKey, timeout)
	}
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	// Start the reader. If this HTTP request is canceled, r.Context().Done() acts as cancellation signal to the underlying UDT connection.
	reader, fileSize, transferSize, err := FileStartReader(peer, fileHash, uint64(offset), uint64(limit), r.Context().Done())
	if reader != nil {
		defer reader.Close()
	}
	if err != nil || reader == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// set the right headers
	setContentLengthRangeHeader(w, uint64(offset), transferSize, fileSize, ranges)

	// Start sending the data!
	io.Copy(w, io.LimitReader(reader, int64(transferSize)))
}

// serveFileFromWarehouse serves the file from the warehouse. If it is not available, it returns false and does not use the writer.
// Limit is optional, 0 means the entire file.
func serveFileFromWarehouse(w http.ResponseWriter, fileHash []byte, offset, limit uint64, ranges []HTTPRange) (valid bool) {
	// Check if the file is available in the local warehouse.
	_, fileInfo, status, _ := core.UserWarehouse.FileExists(fileHash)
	if status != warehouse.StatusOK {
		return false
	}
	fileSize := uint64(fileInfo.Size())

	// validate offset and limit
	if limit > 0 && offset+limit > fileSize {
		http.Error(w, "invalid limit", http.StatusBadRequest)
		return true
	} else if offset > fileSize {
		http.Error(w, "invalid offset", http.StatusBadRequest)
		return true
	} else if limit == 0 {
		limit = fileSize - offset
	}

	setContentLengthRangeHeader(w, offset, limit, uint64(fileInfo.Size()), ranges)

	status, _, _ = core.UserWarehouse.ReadFile(fileHash, int64(offset), int64(limit), w)

	// StatusErrorReadFile must be considered success, since parts of the file may have been transferred already and recovery is not possible.
	return status == warehouse.StatusErrorReadFile || status == warehouse.StatusOK
}

/*
apiFileView is similar to /file/read but but provides a format parameter. It sets the Content-Type and Accept-Ranges headers.
This endpoint supports the Range, Content-Range and Content-Length headers. Multipart ranges are not supported and result in HTTP 400.
Instead of providing the node ID, the peer ID is also accepted in the &node= parameter.
The default timeout for connecting to the peer is 10 seconds.
Formats: 14 = Video

Request:    GET /file/view?hash=[hash]&node=[node ID]&format=[format]
            Optional: &offset=[offset]&limit=[limit] or via Range header.
            Optional: &timeout=[seconds]
Response:   200 with the content
            206 with partial content
            400 if the parameters are invalid
            404 if the file was not found or other error on transfer initiate
            502 if unable to find or connect to the remote peer in time
*/
func apiFileView(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	var err error

	// validate hashes (must be blake3) and other input
	fileHash, valid1 := DecodeBlake3Hash(r.Form.Get("hash"))
	nodeID, valid2 := DecodeBlake3Hash(r.Form.Get("node"))
	publicKey, err3 := core.PublicKeyFromPeerID(r.Form.Get("node"))
	if !valid1 || (!valid2 && err3 != nil) {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	timeoutSeconds, _ := strconv.Atoi(r.Form.Get("timeout"))
	if timeoutSeconds == 0 {
		timeoutSeconds = 10
	}
	timeout := time.Duration(timeoutSeconds) * time.Second

	offset, _ := strconv.Atoi(r.Form.Get("offset"))
	limit, _ := strconv.Atoi(r.Form.Get("limit"))
	format, _ := strconv.Atoi(r.Form.Get("format"))

	// Range header?
	var ranges []HTTPRange
	if ranges, err = ParseRangeHeader(r.Header.Get("Range"), -1, true); err != nil || len(ranges) > 1 {
		http.Error(w, "", http.StatusBadRequest)
		return
	} else if len(ranges) == 1 {
		if ranges[0].length != -1 { // if length is not specified, limit remains 0 which is maximum
			limit = ranges[0].length
		}
		offset = ranges[0].start
	}

	w.Header().Set("Accept-Ranges", "bytes") // always indicate accepting of Range header

	switch format {
	case 14:
		// Video: Indicate MP4 always. There are tons of other MIME types that could be used.
		w.Header().Set("Content-Type", "video/mp4")
	}

	// Is the file available in the local warehouse? In that case requesting it from the remote is unnecessary.
	if serveFileFromWarehouse(w, fileHash, uint64(offset), uint64(limit), ranges) {
		return
	}

	// try connecting via node ID or peer ID?
	var peer *core.PeerInfo

	if valid2 {
		peer, err = PeerConnectNode(nodeID, timeout)
	} else if err3 == nil {
		peer, err = PeerConnectPublicKey(publicKey, timeout)
	}
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	// start the reader
	reader, fileSize, transferSize, err := FileStartReader(peer, fileHash, uint64(offset), uint64(limit), r.Context().Done())
	if reader != nil {
		defer reader.Close()
	}
	if err != nil || reader == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// set the right headers
	setContentLengthRangeHeader(w, uint64(offset), transferSize, fileSize, ranges)

	// Start sending the data!
	io.Copy(w, io.LimitReader(reader, int64(transferSize)))
}

// PeerConnectPublicKey attempts to connect to the peer specified by its public key (= peer ID).
func PeerConnectPublicKey(publicKey *btcec.PublicKey, timeout time.Duration) (peer *core.PeerInfo, err error) {
	if publicKey == nil {
		return nil, errors.New("invalid public key")
	}

	// First look up in the peer list.
	if peer = core.PeerlistLookup(publicKey); peer != nil {
		return peer, nil
	}

	// Try to connect via DHT.
	nodeID := protocol.PublicKey2NodeID(publicKey)
	if _, peer, _ = core.FindNode(nodeID, timeout); peer != nil {
		return peer, nil
	}

	// otherwise not found :(
	return nil, errors.New("peer not found")
}

// PeerConnectNode tries to connect via the node ID
func PeerConnectNode(nodeID []byte, timeout time.Duration) (peer *core.PeerInfo, err error) {
	if len(nodeID) == 256/8 {
		return nil, errors.New("invalid node ID")
	}

	// Try to connect via DHT.
	if _, peer, _ = core.FindNode(nodeID, timeout); peer != nil {
		return peer, nil
	}

	// otherwise not found :(
	return nil, errors.New("peer not found")
}

// FileStartReader providers a reader to a remote file. The reader must be closed by the caller.
// File Size is the full file size reported by the remote peer, regardless of the requested offset and limit. Limit is optional (0 means the entire file).
// Transfer Size is the size in bytes that is actually going to be transferred. The reader should be closed after reading that amount.
// The optional cancelChan can be used to stop the file transfer at any point.
func FileStartReader(peer *core.PeerInfo, hash []byte, offset, limit uint64, cancelChan <-chan struct{}) (reader io.ReadCloser, fileSize, transferSize uint64, err error) {
	if peer == nil {
		return nil, 0, 0, errors.New("peer not provided")
	} else if !peer.IsConnectionActive() {
		return nil, 0, 0, errors.New("no valid connection to peer")
	}

	udtConn, _, err := peer.FileTransferRequestUDT(hash, offset, limit)
	if err != nil {
		return nil, 0, 0, err
	}

	if cancelChan != nil {
		go func() {
			<-cancelChan
			udtConn.Close()
		}()
	}

	fileSize, transferSize, err = protocol.FileTransferReadHeader(udtConn)
	if err != nil {
		udtConn.Close()
		return nil, 0, 0, err
	}

	return udtConn, fileSize, transferSize, nil
}

// FileReadAll downloads the file from the peer.
// This function should only be used for testing or as a basis to fork. The caller should develop a custom download function that handles timeouts and excessive file sizes.
// It allocates whatever size is reported by the remote peer. This could lead to an out of memory crash.
// This function is blocking and may take a long time depending on the remote peer and the network connection.
func FileReadAll(peer *core.PeerInfo, hash []byte) (data []byte, err error) {
	reader, _, transferSize, err := FileStartReader(peer, hash, 0, 0, nil)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	// read all data
	data = make([]byte, transferSize) // Warning: This could lead to an out of memory crash.
	_, err = reader.Read(data)

	// Note: This function does not verify if the returned data matches the hash and expected size.

	return data, err
}
