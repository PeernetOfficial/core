/*
File Name:  Shared Recent.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner
*/
package webapi

import (
	"fmt"

	"github.com/PeernetOfficial/core"
	"github.com/PeernetOfficial/core/blockchain"
)

// queryRecentShared returns recently shared files on the network from random peers until the limit is reached.
func (api *WebapiInstance) queryRecentShared(backend *core.Backend, fileType int, limitPeer, offsetTotal, limitTotal uint64) (files []blockchain.BlockRecordFile) {
	if limitPeer == 0 {
		limitPeer = 1
	}

	// Use the peer list to know about active peers. Random order!
	peerList := api.Backend.PeerlistGet()

	// Files from peers exceeding the limit. It is used if from all peers the total limit is not reached.
	var filesSeconday []blockchain.BlockRecordFile

	for _, peer := range peerList {
		if peer.BlockchainHeight == 0 {
			continue
		}

		var filesFromPeer uint64

		// decode blocks from top down
	blockLoop:
		for blockN := peer.BlockchainHeight - 1; blockN > 0; blockN-- {
			blockDecoded, _, found, _ := backend.ReadBlock(peer.PublicKey, peer.BlockchainVersion, blockN)
			if !found {
				continue
			}

			for _, record := range blockDecoded.RecordsDecoded {
				if file, ok := record.(blockchain.BlockRecordFile); ok && isFileTypeMatchBlock(&file, fileType) {
					// add the tags 'Shared By Count' and 'Shared By GeoIP'
					file.Tags = append(file.Tags, blockchain.TagFromNumber(blockchain.TagSharedByCount, 1))
					if latitude, longitude, valid := api.Peer2GeoIP(peer); valid {
						sharedByGeoIP := fmt.Sprintf("%.4f", latitude) + "," + fmt.Sprintf("%.4f", longitude)
						file.Tags = append(file.Tags, blockchain.TagFromText(blockchain.TagSharedByGeoIP, sharedByGeoIP))
					}

					// found a new file! append.
					if filesFromPeer < limitPeer {
						filesFromPeer++

						if offsetTotal > 0 {
							offsetTotal--
							continue
						}

						files = append(files, file)

						if uint64(len(files)) >= limitTotal {
							return
						}
					} else if uint64(len(filesSeconday)) < limitTotal-uint64(len(files)) {
						filesSeconday = append(filesSeconday, file)
					} else {
						break blockLoop
					}
				}
			}
		}
	}

	files = append(files, filesSeconday...)

	return
}

// isFileTypeMatchBlock checks if the file type matches. -1 = accept any. -2 = core.TypeBinary, core.TypeCompressed, core.TypeContainer, core.TypeExecutable.
func isFileTypeMatchBlock(file *blockchain.BlockRecordFile, fileType int) bool {
	if fileType == -1 {
		return true
	} else if fileType == -2 {
		return file.Type == core.TypeBinary || file.Type == core.TypeCompressed || file.Type == core.TypeContainer || file.Type == core.TypeExecutable
	}

	return file.Type == uint8(fileType)
}
