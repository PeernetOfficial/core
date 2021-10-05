/*
File Name:  Blockchain.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/

package webapi

import (
	"encoding/hex"
	"net/http"
	"strconv"

	"github.com/PeernetOfficial/core"
)

const dateFormat = "2006-01-02 15:04:05"

type apiBlockchainHeader struct {
	PeerID  string `json:"peerid"`  // Peer ID hex encoded.
	Version uint64 `json:"version"` // Current version number of the blockchain.
	Height  uint64 `json:"height"`  // Height of the blockchain (number of blocks). If 0, no data exists.
}

/*
apiBlockchainSelfHeader returns the current blockchain header information

Request:    GET /blockchain/self/header
Result:     200 with JSON structure apiResponsePeerSelf
*/
func apiBlockchainSelfHeader(w http.ResponseWriter, r *http.Request) {
	publicKey, height, version := core.UserBlockchainHeader()

	EncodeJSON(w, r, apiBlockchainHeader{Version: version, Height: height, PeerID: hex.EncodeToString(publicKey.SerializeCompressed())})
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
	Status  int    `json:"status"`  // Status: 0 = Success, 1 = Error invalid data
	Height  uint64 `json:"height"`  // Height of the blockchain (number of blocks).
	Version uint64 `json:"version"` // Version of the blockchain.
}

/*
apiBlockchainSelfAppend appends a block to the blockchain. This is a low-level function for already encoded blocks.
Do not use this function. Adding invalid data to the blockchain may corrupt it which might result in blacklisting by other peers.

Request:    POST /blockchain/self/append with JSON structure apiBlockchainBlockRaw
Response:   200 with JSON structure apiBlockchainBlockStatus
*/
func apiBlockchainSelfAppend(w http.ResponseWriter, r *http.Request) {
	var input apiBlockchainBlockRaw
	if err := DecodeJSON(w, r, &input); err != nil {
		return
	}

	var records []core.BlockRecordRaw

	for _, record := range input.Records {
		records = append(records, core.BlockRecordRaw{Type: record.Type, Data: record.Data})
	}

	newHeight, newVersion, status := core.UserBlockchainAppend(records)

	EncodeJSON(w, r, apiBlockchainBlockStatus{Status: status, Height: newHeight, Version: newVersion})
}

type apiBlockchainBlock struct {
	Status            int                 `json:"status"`            // Status: 0 = Success, 1 = Error block not found, 2 = Error block encoding (indicates that the blockchain is corrupt)
	PeerID            string              `json:"peerid"`            // Peer ID hex encoded.
	LastBlockHash     []byte              `json:"lastblockhash"`     // Hash of the last block. Blake3.
	BlockchainVersion uint64              `json:"blockchainversion"` // Blockchain version
	Number            uint64              `json:"blocknumber"`       // Block number
	RecordsRaw        []apiBlockRecordRaw `json:"recordsraw"`        // Records raw. Successfully decoded records are parsed into the below fields.
	RecordsDecoded    []interface{}       `json:"recordsdecoded"`    // Records decoded. The encoding for each record depends on its type.
}

/*
apiBlockchainSelfRead reads a block and returns the decoded information.

Request:    GET /blockchain/self/read?block=[number]
Result:     200 with JSON structure apiBlockchainBlock
*/
func apiBlockchainSelfRead(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	blockN, err := strconv.Atoi(r.Form.Get("block"))
	if err != nil || blockN < 0 {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	block, status, _ := core.UserBlockchainRead(uint64(blockN))
	result := apiBlockchainBlock{Status: status}

	if status == 0 {
		for _, record := range block.RecordsRaw {
			result.RecordsRaw = append(result.RecordsRaw, apiBlockRecordRaw{Type: record.Type, Data: record.Data})
		}

		result.PeerID = hex.EncodeToString(block.OwnerPublicKey.SerializeCompressed())

		for _, record := range block.RecordsDecoded {
			switch v := record.(type) {
			case core.BlockRecordFile:
				result.RecordsDecoded = append(result.RecordsDecoded, blockRecordFileToAPI(v))

			case core.BlockRecordProfile:
				result.RecordsDecoded = append(result.RecordsDecoded, blockRecordProfileToAPI(v))

			}
		}
	}

	EncodeJSON(w, r, result)
}
