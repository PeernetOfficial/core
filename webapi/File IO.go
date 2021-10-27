/*
File Name:  File IO.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/PeernetOfficial/core"
	"github.com/PeernetOfficial/core/protocol"
	"github.com/btcsuite/btcd/btcec"
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

	reader, fileSize, transferSize, err := FileStartReader(peer, fileHash, uint64(offset), uint64(limit))
	if reader != nil {
		defer reader.Close()
	}
	if err != nil || reader == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	fmt.Printf("SUCCESS file size is %d transfer size is %d\n", fileSize, transferSize)

	// Set the Content-Length header, always to the actual size of transferred data.
	w.Header().Set("Content-Length", strconv.FormatUint(transferSize, 10))

	// Set the Content-Range header if needed.
	if len(ranges) == 1 {
		w.Header().Set("Content-Range", "bytes "+strconv.Itoa(offset)+"-"+strconv.Itoa(offset+int(transferSize)-1)+"/"+strconv.FormatUint(fileSize, 10))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}

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
	return nil, errors.New("peer not found :(")
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

	return nil, nil
}

// FileStartReader providers a reader to a remote file. The reader must be closed by the caller.
// File Size is the full file size, regardless of the requested offset and limit.
// Transfer Size is the size in bytes that is actually going to be transferred. The reader should be closed after reading that amount.
func FileStartReader(peer *core.PeerInfo, hash []byte, offset, limit uint64) (reader io.ReadCloser, fileSize, transferSize uint64, err error) {
	if peer == nil {
		return nil, 0, 0, errors.New("peer not provided")
	} else if !peer.IsConnectionActive() {
		return nil, 0, 0, errors.New("no valid connection to peer")
	}

	udtConn, err := peer.FileTransferRequestUDT(hash, offset, limit)
	if err != nil {
		return nil, 0, 0, err
	}

	fileSize, transferSize, err = core.FileTransferReadHeaderUDT(udtConn)
	if err != nil {
		udtConn.Close()
		return nil, 0, 0, err
	}

	return udtConn, fileSize, transferSize, nil
}
