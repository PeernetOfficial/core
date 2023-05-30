/*
File Username:  Blockchain.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"encoding/hex"
	"net/http"
	"strconv"

	"github.com/PeernetOfficial/core/blockchain"
)

type apiBlockchainHeader struct {
	PeerID  string `json:"peerid"`  // Peer ID hex encoded.
	Version uint64 `json:"version"` // Current version number of the blockchain.
	Height  uint64 `json:"height"`  // Height of the blockchain (number of blocks). If 0, no data exists.
}

/*
apiBlockchainHeaderFunc returns the current blockchain header information

Request:    GET /blockchain/header
Result:     200 with JSON structure apiResponsePeerSelf
*/
func (api *WebapiInstance) apiBlockchainHeaderFunc(w http.ResponseWriter, r *http.Request) {
	publicKey, height, version := api.Backend.UserBlockchain.Header()

	EncodeJSON(api.Backend, w, r, apiBlockchainHeader{Version: version, Height: height, PeerID: hex.EncodeToString(publicKey.SerializeCompressed())})
}

type apiBlockRecordRaw struct {
	Type uint8  `json:"type"` // Record Type. See core.RecordTypeX.
	Data []byte `json:"data"` // Data according to the type.
}

// apiBlockchainBlockRaw contains a raw block of the blockchain via API
type apiBlockchainBlockRaw struct {
	Records []apiBlockRecordRaw `json:"records"` // Block records in encoded raw format.
}

type apiBlockchainBlockStatus struct {
	Status  int    `json:"status"`  // See blockchain.StatusX.
	Height  uint64 `json:"height"`  // Height of the blockchain (number of blocks).
	Version uint64 `json:"version"` // Version of the blockchain.
}

/*
apiBlockchainAppend appends a block to the blockchain. This is a low-level function for already encoded blocks.
Do not use this function. Adding invalid data to the blockchain may corrupt it which might result in blacklisting by other peers.

Request:    POST /blockchain/append with JSON structure apiBlockchainBlockRaw
Response:   200 with JSON structure apiBlockchainBlockStatus
*/
func (api *WebapiInstance) apiBlockchainAppend(w http.ResponseWriter, r *http.Request) {
	var input apiBlockchainBlockRaw
	if err := DecodeJSON(w, r, &input); err != nil {
		return
	}

	var records []blockchain.BlockRecordRaw

	for _, record := range input.Records {
		records = append(records, blockchain.BlockRecordRaw{Type: record.Type, Data: record.Data})
	}

	newHeight, newVersion, status := api.Backend.UserBlockchain.Append(records)

	EncodeJSON(api.Backend, w, r, apiBlockchainBlockStatus{Status: status, Height: newHeight, Version: newVersion})
}

type apiBlockchainBlock struct {
	Status            int                 `json:"status"`            // See blockchain.StatusX.
	PeerID            string              `json:"peerid"`            // Peer ID hex encoded.
	LastBlockHash     []byte              `json:"lastblockhash"`     // Hash of the last block. Blake3.
	BlockchainVersion uint64              `json:"blockchainversion"` // Blockchain version
	Number            uint64              `json:"blocknumber"`       // Block number
	RecordsRaw        []apiBlockRecordRaw `json:"recordsraw"`        // Records raw. Successfully decoded records are parsed into the below fields.
	RecordsDecoded    []interface{}       `json:"recordsdecoded"`    // Records decoded. The encoding for each record depends on its type.
}

/*
apiBlockchainRead reads a block and returns the decoded information.

Request:    GET /blockchain/read?block=[number]
Result:     200 with JSON structure apiBlockchainBlock
*/
func (api *WebapiInstance) apiBlockchainRead(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	blockN, err := strconv.Atoi(r.Form.Get("block"))
	if err != nil || blockN < 0 {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	block, status, _ := api.Backend.UserBlockchain.Read(uint64(blockN))
	result := apiBlockchainBlock{Status: status}

	if status == 0 {
		for _, record := range block.RecordsRaw {
			result.RecordsRaw = append(result.RecordsRaw, apiBlockRecordRaw{Type: record.Type, Data: record.Data})
		}

		result.PeerID = hex.EncodeToString(block.OwnerPublicKey.SerializeCompressed())

		for _, record := range block.RecordsDecoded {
			switch v := record.(type) {
			case blockchain.BlockRecordFile:
				result.RecordsDecoded = append(result.RecordsDecoded, blockRecordFileToAPI(v))

			case blockchain.BlockRecordProfile:
				result.RecordsDecoded = append(result.RecordsDecoded, blockRecordProfileToAPI(v))

			}
		}
	}

	EncodeJSON(api.Backend, w, r, result)
}

/*
apiExploreNodeID returns the shared files of a particular node in Peernet. Results are returned in real-time. The file type is an optional filter. See TypeX.
Special type -2 = Binary, Compressed, Container, Executable. This special type includes everything except Documents, Video, Audio, Ebooks, Picture, Text.

Request:    GET /blockchain/view?limit=[max records]&type=[file type]&offset=[offset]&node=[node id]
Result:     200 with JSON structure SearchResult. Check the field status.
*/
func (api *WebapiInstance) apiExploreNodeID(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	offset, _ := strconv.Atoi(r.Form.Get("offset"))
	limit, err := strconv.Atoi(r.Form.Get("limit"))
	if err != nil {
		limit = 100
	}
	// ID fields for results for a specific node ID.
	NodeId, _ := DecodeBlake3Hash(r.Form.Get("node"))

	fileType, err := strconv.Atoi(r.Form.Get("type"))
	if err != nil {
		fileType = -1
	}

	result := api.ExploreHelper(fileType, limit, offset, NodeId, true)

	EncodeJSON(api.Backend, w, r, result)
}
