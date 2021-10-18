/*
File Name:  Message Encoding Response.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"time"
	"unicode/utf8"

	"github.com/btcsuite/btcd/btcec"
)

// MessageResponse is the decoded response message.
type MessageResponse struct {
	*MessageRaw                          // Underlying raw message
	Protocol          uint8              // Protocol version supported (low 4 bits).
	Features          uint8              // Feature support (high 4 bits). Future use.
	Actions           uint8              // Action bit array. See ActionX
	BlockchainHeight  uint32             // Blockchain height
	BlockchainVersion uint64             // Blockchain version
	PortInternal      uint16             // Internal port. Can be used to detect NATs.
	PortExternal      uint16             // External port if known. 0 if not. Can be used for UPnP support.
	UserAgent         string             // User Agent. Format "Software/Version". Required in the initial announcement/bootstrap. UTF-8 encoded. Max length is 255 bytes.
	Hash2Peers        []Hash2Peer        // List of peers that know the requested hashes or at least are close to it
	FilesEmbed        []EmbeddedFileData // Files that were embedded in the response
	HashesNotFound    [][]byte           // Hashes that were reported back as not found
}

// PeerRecord informs about a peer
type PeerRecord struct {
	PublicKey                *btcec.PublicKey // Public Key
	NodeID                   []byte           // Kademlia Node ID
	IPv4                     net.IP           // IPv4 address. 0 if not set.
	IPv4Port                 uint16           // Port (actual one used for connection)
	IPv4PortReportedInternal uint16           // Internal port as reported by that peer. This can be used to identify whether the peer is potentially behind a NAT.
	IPv4PortReportedExternal uint16           // External port as reported by that peer. This is used in case of port forwarding (manual or automated).
	IPv6                     net.IP           // IPv6 address. 0 if not set.
	IPv6Port                 uint16           // Port (actual one used for connection)
	IPv6PortReportedInternal uint16           // Internal port as reported by that peer. This can be used to identify whether the peer is potentially behind a NAT.
	IPv6PortReportedExternal uint16           // External port as reported by that peer. This is used in case of port forwarding (manual or automated).
	LastContact              uint32           // Last contact in seconds
	LastContactT             time.Time        // Last contact time translated from seconds
}

// Hash2Peer links a hash to peers who are known to store the data and to peers who are considered close to the hash
type Hash2Peer struct {
	ID      KeyHash      // Hash that was queried
	Closest []PeerRecord // Closest peers
	Storing []PeerRecord // Peers known to store the data identified by the hash
	IsLast  bool         // Whether it is the last records returned for the requested hash and no more results will follow
}

// EmbeddedFileData contains embedded data sent within a response
type EmbeddedFileData struct {
	ID   KeyHash // Hash of the file
	Data []byte  // Data
}

// Actions in Response message
const (
	ActionSequenceLast = 0 // SEQUENCE_LAST Last response to the announcement in the sequence
)

// DecodeResponse decodes the incoming response message. Returns nil if invalid.
func DecodeResponse(msg *MessageRaw) (result *MessageResponse, err error) {
	result = &MessageResponse{
		MessageRaw: msg,
	}

	if len(msg.Payload) < announcementPayloadHeaderSize+6 {
		return nil, errors.New("response: invalid minimum length")
	}

	result.Protocol = msg.Payload[0] & 0x0F // Protocol version support is stored in the first 4 bits
	result.Features = msg.Payload[1]        // Feature support
	result.Actions = msg.Payload[2]
	result.BlockchainHeight = binary.LittleEndian.Uint32(msg.Payload[3:7])
	result.BlockchainVersion = binary.LittleEndian.Uint64(msg.Payload[7:15])
	result.PortInternal = binary.LittleEndian.Uint16(msg.Payload[15:17])
	result.PortExternal = binary.LittleEndian.Uint16(msg.Payload[17:19])

	userAgentLength := int(msg.Payload[19])
	read := announcementPayloadHeaderSize

	if userAgentLength > 0 {
		if userAgentLength > len(msg.Payload)-announcementPayloadHeaderSize {
			return nil, errors.New("response: user agent overflow")
		}

		userAgentB := msg.Payload[announcementPayloadHeaderSize : announcementPayloadHeaderSize+userAgentLength]
		if !utf8.Valid(userAgentB) {
			return nil, errors.New("response: user agent invalid encoding")
		}

		result.UserAgent = string(userAgentB)
		read += userAgentLength
	}

	countPeerResponses := binary.LittleEndian.Uint16(msg.Payload[read+0 : read+0+2])
	countEmbeddedFiles := binary.LittleEndian.Uint16(msg.Payload[read+2 : read+2+2])
	countHashesNotFound := binary.LittleEndian.Uint16(msg.Payload[read+4 : read+4+2])
	read += 6

	if countPeerResponses == 0 && countEmbeddedFiles == 0 && countHashesNotFound == 0 {
		return nil, errors.New("response: empty")
	}

	data := msg.Payload[read:]

	// Peer response data
	if countPeerResponses > 0 {
		hash2Peers, read, valid := decodePeerRecord(data, int(countPeerResponses))
		if !valid {
			return nil, errors.New("response: peer info invalid data")
		}
		data = data[read:]

		result.Hash2Peers = append(result.Hash2Peers, hash2Peers...)
	}

	// Embedded files
	if countEmbeddedFiles > 0 {
		filesEmbed, read, valid := decodeEmbeddedFile(data, int(countEmbeddedFiles))
		if !valid {
			return nil, errors.New("response: embedded file invalid data")
		}
		data = data[read:]

		result.FilesEmbed = append(result.FilesEmbed, filesEmbed...)
	}

	// Hashes not found
	if countHashesNotFound > 0 {
		if len(data) < int(countHashesNotFound)*32 {
			return nil, errors.New("response: hash list invalid data")
		}

		for n := 0; n < int(countHashesNotFound); n++ {
			hash := make([]byte, HashSize)
			copy(hash, data[n*32:n*32+32])

			result.HashesNotFound = append(result.HashesNotFound, hash)
		}
	}

	return
}

// Length of peer record in bytes
const peerRecordSize = 70

// decodePeerRecord decodes the response data for FIND_SELF, FIND_PEER and FIND_VALUE messages
func decodePeerRecord(data []byte, count int) (hash2Peers []Hash2Peer, read int, valid bool) {
	index := 0

	for n := 0; n < count; n++ {
		if read += 34; len(data) < read {
			return nil, 0, false
		}

		hash := make([]byte, HashSize)
		copy(hash, data[index:index+32])
		countField := binary.LittleEndian.Uint16(data[index+32:index+32+2]) & 0x7FFF
		isLast := binary.LittleEndian.Uint16(data[index+32:index+32+2])&0x8000 > 0
		index += 34

		hash2Peer := Hash2Peer{ID: KeyHash{hash}, IsLast: isLast}

		// Response contains peer records
		for m := 0; m < int(countField); m++ {
			if read += peerRecordSize; len(data) < read {
				return nil, 0, false
			}

			peer := PeerRecord{}

			peerIDcompressed := make([]byte, 33)
			copy(peerIDcompressed[:], data[index:index+33])

			// IPv4
			ipv4B := make([]byte, 4)
			copy(ipv4B[:], data[index+33:index+33+4])

			peer.IPv4 = ipv4B
			peer.IPv4Port = binary.LittleEndian.Uint16(data[index+37 : index+37+2])
			peer.IPv4PortReportedInternal = binary.LittleEndian.Uint16(data[index+39 : index+39+2])
			peer.IPv4PortReportedExternal = binary.LittleEndian.Uint16(data[index+41 : index+41+2])

			// IPv6
			ipv6B := make([]byte, 16)
			copy(ipv6B[:], data[index+43:index+43+16])

			peer.IPv6 = ipv6B
			peer.IPv6Port = binary.LittleEndian.Uint16(data[index+59 : index+59+2])
			peer.IPv6PortReportedInternal = binary.LittleEndian.Uint16(data[index+61 : index+61+2])
			peer.IPv6PortReportedExternal = binary.LittleEndian.Uint16(data[index+63 : index+63+2])

			if peer.IPv6.To4() != nil { // IPv6 address mismatch
				return nil, 0, false
			}

			peer.LastContact = binary.LittleEndian.Uint32(data[index+65 : index+65+4])
			peer.LastContactT = time.Now().Add(-time.Second * time.Duration(peer.LastContact))
			reason := data[index+69]

			var err error
			if peer.PublicKey, err = btcec.ParsePubKey(peerIDcompressed, btcec.S256()); err != nil {
				return nil, 0, false
			}

			peer.NodeID = PublicKey2NodeID(peer.PublicKey)

			if reason == 0 { // Peer was returned because it is close to the requested hash
				hash2Peer.Closest = append(hash2Peer.Closest, peer)
			} else if reason == 1 { // Peer stores the data
				hash2Peer.Storing = append(hash2Peer.Storing, peer)
			}

			index += peerRecordSize
		}

		hash2Peers = append(hash2Peers, hash2Peer)
	}

	return hash2Peers, read, true
}

// decodeEmbeddedFile decodes the embedded file response data for FIND_VALUE
func decodeEmbeddedFile(data []byte, count int) (filesEmbed []EmbeddedFileData, read int, valid bool) {
	index := 0

	for n := 0; n < count; n++ {
		if read += 34; len(data) < read {
			return nil, 0, false
		}

		hash := make([]byte, HashSize)
		copy(hash, data[index:index+32])
		sizeField := int(binary.LittleEndian.Uint16(data[index+32 : index+32+2]))
		index += 34

		if read += sizeField; len(data) < read {
			return nil, 0, false
		}

		fileData := make([]byte, sizeField)
		copy(fileData[:], data[index:index+sizeField])

		index += sizeField

		// validate the hash
		if !bytes.Equal(hash, HashData(fileData)) {
			return nil, read, false
		}

		filesEmbed = append(filesEmbed, EmbeddedFileData{ID: KeyHash{Hash: hash}, Data: fileData})
	}

	return filesEmbed, read, true
}

// EmbeddedFileSizeMax is the maximum size of embedded files in response messages. Any file exceeding that must be shared via regular file transfer.
const EmbeddedFileSizeMax = udpMaxPacketSize - PacketLengthMin - announcementPayloadHeaderSize - 2 - 35

// EncodeResponse encodes a response message
// hash2Peers will be modified.
func EncodeResponse(sendUA bool, hash2Peers []Hash2Peer, filesEmbed []EmbeddedFileData, hashesNotFound [][]byte, features byte, blockchainHeight, blockchainVersion uint64, userAgent string) (packetsRaw [][]byte, err error) {
	for n := range filesEmbed {
		if len(filesEmbed[n].Data) > EmbeddedFileSizeMax {
			return nil, errors.New("embedded file too big")
		}
	}

createPacketLoop:
	for {
		raw := make([]byte, 64*1024) // max UDP packet size
		packetSize := announcementPayloadHeaderSize

		raw[0] = byte(ProtocolVersion) // Protocol
		raw[1] = features              // Feature support
		//raw[2] = Actions                                   // Action bit array

		binary.LittleEndian.PutUint32(raw[3:7], uint32(blockchainHeight))
		binary.LittleEndian.PutUint64(raw[7:15], blockchainVersion)

		// only on initial response the User Agent must be provided according to the protocol spec
		if sendUA {
			userAgentB := []byte(userAgent)
			if len(userAgentB) > 255 {
				userAgentB = userAgentB[:255]
			}

			raw[19] = byte(len(userAgentB))
			copy(raw[announcementPayloadHeaderSize:announcementPayloadHeaderSize+len(userAgentB)], userAgentB)
			packetSize += len(userAgentB)
		}

		// 3 count field at raw[index]: count of peer responses, embedded files, and hashes not found
		countIndex := packetSize
		packetSize += 6

		// Encode the peer response data for FIND_SELF, FIND_PEER and FIND_VALUE requests.
		if len(hash2Peers) > 0 {
			for n, hash2Peer := range hash2Peers {
				if isPacketSizeExceed(packetSize, 34+peerRecordSize) { // check if minimum length is available in packet
					packetsRaw = append(packetsRaw, raw[:packetSize])
					hash2Peers = hash2Peers[n:]
					continue createPacketLoop
				}

				index := packetSize
				copy(raw[index:index+32], hash2Peer.ID.Hash)
				count2Index := index + 32

				packetSize += 34
				count2 := uint16(0)

				for m := range hash2Peer.Storing {
					if isPacketSizeExceed(packetSize, peerRecordSize) { // check if minimum length is available in packet
						packetsRaw = append(packetsRaw, raw[:packetSize])
						hash2Peers = hash2Peers[n:]
						hash2Peer.Storing = hash2Peer.Storing[m:]
						continue createPacketLoop
					}

					index := packetSize
					encodePeerRecord(raw[index:index+peerRecordSize], &hash2Peer.Storing[m], 1)

					packetSize += peerRecordSize
					binary.LittleEndian.PutUint16(raw[count2Index+0:count2Index+2], uint16(m+1))
					count2++
				}

				hash2Peer.Storing = nil

				for m := range hash2Peer.Closest {
					if isPacketSizeExceed(packetSize, peerRecordSize) { // check if minimum length is available in packet
						packetsRaw = append(packetsRaw, raw[:packetSize])
						hash2Peers = hash2Peers[n:]
						hash2Peer.Closest = hash2Peer.Closest[m:]
						continue createPacketLoop
					}

					index := packetSize
					encodePeerRecord(raw[index:index+peerRecordSize], &hash2Peer.Closest[m], 0)

					packetSize += peerRecordSize
					count2++
					binary.LittleEndian.PutUint16(raw[count2Index+0:count2Index+2], count2)
				}

				binary.LittleEndian.PutUint16(raw[count2Index+0:count2Index+2], count2|0x8000) // signal the last result for the key with bit 15
				binary.LittleEndian.PutUint16(raw[countIndex+0:countIndex+0+2], uint16(n+1))   // count of peer responses
			}

			hash2Peers = nil
		}

		// FIND_VALUE response embedded data
		if len(filesEmbed) > 0 {
			if isPacketSizeExceed(packetSize, 34+len(filesEmbed[0].Data)) { // check if there is enough space for at least the header and 1 record
				packetsRaw = append(packetsRaw, raw[:packetSize])
				continue createPacketLoop
			}

			for n, file := range filesEmbed {
				if isPacketSizeExceed(packetSize, 34+len(file.Data)) { // check if minimum length is available in packet
					packetsRaw = append(packetsRaw, raw[:packetSize])
					filesEmbed = filesEmbed[n:]
					continue createPacketLoop
				}

				index := packetSize
				copy(raw[index:index+32], file.ID.Hash)
				binary.LittleEndian.PutUint16(raw[index+32:index+32+2], uint16(len(file.Data)))
				copy(raw[index+34:index+34+len(file.Data)], file.Data)

				binary.LittleEndian.PutUint16(raw[countIndex+2:countIndex+2+2], uint16(n+1)) // count of embedded files
				packetSize += 34 + len(file.Data)
			}

			filesEmbed = nil
		}

		// Hashes not found
		if len(hashesNotFound) > 0 {
			index := packetSize

			for n, hash := range hashesNotFound {
				if isPacketSizeExceed(packetSize, 32) { // check if there is enough space for at least the header and 1 record
					packetsRaw = append(packetsRaw, raw[:packetSize])
					continue createPacketLoop
				}

				copy(raw[index+n*32:index+n*32+32], hash)

				binary.LittleEndian.PutUint16(raw[countIndex+4:countIndex+4+2], uint16(n+1)) // count of hashes not found
				packetSize += 32
			}

			hashesNotFound = nil
		}

		raw[2] |= 1 << ActionSequenceLast // Indicate that no more responses will be sent in this sequence
		packetsRaw = append(packetsRaw, raw[:packetSize])

		if len(hash2Peers) == 0 && len(filesEmbed) == 0 && len(hashesNotFound) == 0 { // this should always be the case here
			return
		}
	}
}

// encodePeerRecord encodes a single peer record and stores it into raw
func encodePeerRecord(raw []byte, peer *PeerRecord, reason uint8) {
	copy(raw[0:0+33], peer.PublicKey.SerializeCompressed())
	binary.LittleEndian.PutUint32(raw[65:65+4], peer.LastContact)
	raw[69] = reason

	// IPv4
	copy(raw[33:33+4], peer.IPv4.To4())
	binary.LittleEndian.PutUint16(raw[37:37+2], peer.IPv4Port)
	binary.LittleEndian.PutUint16(raw[39:39+2], peer.IPv4PortReportedInternal)
	binary.LittleEndian.PutUint16(raw[41:41+2], peer.IPv4PortReportedExternal)

	// IPv6
	copy(raw[43:43+16], peer.IPv6.To16())
	binary.LittleEndian.PutUint16(raw[59:59+2], peer.IPv6Port)
	binary.LittleEndian.PutUint16(raw[61:61+2], peer.IPv6PortReportedInternal)
	binary.LittleEndian.PutUint16(raw[63:63+2], peer.IPv6PortReportedExternal)
}
