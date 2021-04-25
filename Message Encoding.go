/*
File Name:  Message Encoding.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Intermediary between low-level packets and high-level interpretation.
*/

package core

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"time"
	"unicode/utf8"

	"github.com/btcsuite/btcd/btcec"
)

// ProtocolVersion is the current protocol version
const ProtocolVersion = 0

// UserAgent should be set by the caller
var UserAgent = "Peernet Core/0.1"

// Commands between peers
const (
	// Peer List Management
	CommandAnnouncement   = 0 // Announcement
	CommandResponse       = 1 // Response
	CommandPing           = 2 // Keep-alive message (no payload).
	CommandPong           = 3 // Response to ping (no payload).
	CommandLocalDiscovery = 4 // Local discovery

	// Blockchain
	CommandGet = 6 // Request blocks for specified peer.

	// File Discovery

	// Debug
	CommandChat = 10 // Chat message [debug]
)

// Actions between peers, sent via Announcement message. They correspond to the bit array index.
const (
	ActionFindSelf  = 0 // FIND_SELF Request closest neighbors to self
	ActionFindPeer  = 1 // FIND_PEER Request closest neighbors to target peer
	ActionFindValue = 2 // FIND_VALUE Request data or closest peers
	ActionInfoStore = 3 // INFO_STORE Sender indicates storing provided data
)

// Actions in Response message
const (
	ActionSequenceLast = 0 // SEQUENCE_LAST Last response to the announcement in the sequence
)

// Features are sent as bit array in the Announcement message.
const (
	FeatureIPv4Listen = 0 // Sender listens on IPv4
	FeatureIPv6Listen = 1 // Sender listens on IPv6
)

// MessageRaw is a high-level message between peers that has not been decoded
type MessageRaw struct {
	PacketRaw
	SenderPublicKey *btcec.PublicKey // Sender Public Key, ECDSA (secp256k1) 257-bit
	connection      *Connection      // Connection that received the packet
	sequence        *sequenceExpiry  // Sequence
}

// MessageAnnouncement is the decoded announcement message.
type MessageAnnouncement struct {
	*MessageRaw                   // Underlying raw message
	Protocol          uint8       // Protocol version supported (low 4 bits).
	Features          uint8       // Feature support (high 4 bits). Future use.
	Actions           uint8       // Action bit array. See ActionX
	BlockchainHeight  uint32      // Blockchain height
	BlockchainVersion uint64      // Blockchain version
	UserAgent         string      // User Agent. Format "Software/Version". Required in the initial announcement/bootstrap. UTF-8 encoded. Max length is 255 bytes.
	FindPeerKeys      []KeyHash   // FIND_PEER data
	FindDataKeys      []KeyHash   // FIND_VALUE data
	InfoStoreFiles    []InfoStore // INFO_STORE data
}

// blake3 digest size in bytes
const hashSize = 32

// KeyHash is a single blake3 key hash
type KeyHash struct {
	Hash []byte
}

// InfoStore informs about files stored
type InfoStore struct {
	ID   KeyHash // Hash of the file
	Size uint64  // Size of the file
	Type uint8   // Type of the file: 0 = File, 1 = Header file containing list of parts
}

// PeerRecord informs about a peer
type PeerRecord struct {
	PublicKey   *btcec.PublicKey // Public Key
	NodeID      []byte           // Kademlia Node ID
	IP          net.IP           // IP
	Port        uint16           // Port
	LastContact uint32           // Last contact in seconds
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

// MessageResponse is the decoded response message.
type MessageResponse struct {
	*MessageRaw                          // Underlying raw message
	Protocol          uint8              // Protocol version supported (low 4 bits).
	Features          uint8              // Feature support (high 4 bits). Future use.
	Actions           uint8              // Action bit array. See ActionX
	BlockchainHeight  uint32             // Blockchain height
	BlockchainVersion uint64             // Blockchain version
	UserAgent         string             // User Agent. Format "Software/Version". Required in the initial announcement/bootstrap. UTF-8 encoded. Max length is 255 bytes.
	Hash2Peers        []Hash2Peer        // List of peers that know the requested hashes or at least are close to it
	FilesEmbed        []EmbeddedFileData // Files that were embedded in the response
	HashesNotFound    [][]byte           // Hashes that were reported back as not found
}

// ---- message decoding ----

// Minimum length of Announcement payload header without User Agent
const announcementPayloadHeaderSize = 16

// msgDecodeAnnouncement decodes the incoming announcement message. Returns nil if invalid.
func msgDecodeAnnouncement(msg *MessageRaw) (result *MessageAnnouncement, err error) {
	result = &MessageAnnouncement{
		MessageRaw: msg,
	}

	if len(msg.Payload) < announcementPayloadHeaderSize {
		return nil, errors.New("announcement: invalid minimum length")
	}

	result.Protocol = msg.Payload[0] & 0x0F // Protocol version support is stored in the first 4 bits
	result.Features = msg.Payload[1]        // Feature support
	result.Actions = msg.Payload[2]
	result.BlockchainHeight = binary.LittleEndian.Uint32(msg.Payload[3:7])
	result.BlockchainVersion = binary.LittleEndian.Uint64(msg.Payload[7:15])

	userAgentLength := int(msg.Payload[15])
	if userAgentLength > 0 {
		if userAgentLength > len(msg.Payload)-announcementPayloadHeaderSize {
			return nil, errors.New("announcement: user agent overflow")
		}

		userAgentB := msg.Payload[announcementPayloadHeaderSize : announcementPayloadHeaderSize+userAgentLength]
		if !utf8.Valid(userAgentB) {
			return nil, errors.New("announcement: user agent invalid encoding")
		}

		result.UserAgent = string(userAgentB)
	}

	data := msg.Payload[announcementPayloadHeaderSize+userAgentLength:]

	// FIND_PEER
	if result.Actions&(1<<ActionFindPeer) > 0 {
		keys, read, valid := decodeKeys(data)
		if !valid {
			return nil, errors.New("announcement: FIND_PEER invalid data")
		}

		data = data[read:]
		result.FindPeerKeys = keys
	}

	// FIND_VALUE
	if result.Actions&(1<<ActionFindValue) > 0 {
		keys, read, valid := decodeKeys(data)
		if !valid {
			return nil, errors.New("announcement: FIND_VALUE invalid data")
		}

		data = data[read:]
		result.FindDataKeys = keys
	}

	// INFO_STORE
	if result.Actions&(1<<ActionInfoStore) > 0 {
		files, read, valid := decodeInfoStore(data)
		if !valid {
			return nil, errors.New("announcement: INFO_STORE invalid data")
		}

		data = data[read:]
		result.InfoStoreFiles = files
	}

	// Accept extra data in case future features append additional data
	//if len(data) > 0 {
	//	return nil, errors.New("announcement: Unexpected extra data")
	//}

	return
}

// decodeKeys decodes keys. Header is 2 bytes (count) followed by the actual keys (each 32 bytes blake3 hash).
func decodeKeys(data []byte) (keys []KeyHash, read int, valid bool) {
	if len(data) < 2+hashSize { // minimum length
		return nil, 0, false
	}

	count := binary.LittleEndian.Uint16(data[0:2])

	if read = 2 + int(count)*hashSize; len(data) < read {
		return nil, 0, false
	}

	for n := 0; n < int(count); n++ {
		key := make([]byte, hashSize)
		copy(key, data[2+n*hashSize:2+n*hashSize+hashSize])
		keys = append(keys, KeyHash{Hash: key})
	}

	return keys, read, true
}

func decodeInfoStore(data []byte) (files []InfoStore, read int, valid bool) {
	if len(data) < 2+41 { // minimum length
		return nil, 0, false
	}

	count := binary.LittleEndian.Uint16(data[0:2])

	if read = 2 + int(count)*41; len(data) < read {
		return nil, 0, false
	}

	for n := 0; n < int(count); n++ {
		file := InfoStore{}
		file.ID.Hash = make([]byte, hashSize)
		copy(file.ID.Hash, data[2+n*41:2+n*41+hashSize])
		file.Size = binary.LittleEndian.Uint64(data[2+n*41+32 : 2+n*41+32+8])
		file.Type = data[2+n*41+40]

		files = append(files, file)
	}

	return files, read, true
}

// msgDecodeResponse decodes the incoming response message. Returns nil if invalid.
func msgDecodeResponse(msg *MessageRaw) (result *MessageResponse, err error) {
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

	userAgentLength := int(msg.Payload[15])
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
			hash := make([]byte, hashSize)
			copy(hash, data[n*32:n*32+32])

			result.HashesNotFound = append(result.HashesNotFound, hash)
		}
	}

	return
}

// decodePeerRecord decodes the response data for FIND_SELF, FIND_PEER and FIND_VALUE messages
func decodePeerRecord(data []byte, count int) (hash2Peers []Hash2Peer, read int, valid bool) {
	index := 0

	for n := 0; n < count; n++ {
		if read += 34; len(data) < read {
			return nil, 0, false
		}

		hash := make([]byte, hashSize)
		copy(hash, data[index:index+32])
		countField := binary.LittleEndian.Uint16(data[index+32:index+32+2]) & 0x7FFF
		isLast := binary.LittleEndian.Uint16(data[index+32:index+32+2])&0x8000 > 0
		index += 34

		hash2Peer := Hash2Peer{ID: KeyHash{hash}, IsLast: isLast}

		// Response contains peer records
		for m := 0; m < int(countField); m++ {
			if read += 56; len(data) < read {
				return nil, 0, false
			}

			peer := PeerRecord{}

			peerIDcompressed := make([]byte, 33)
			copy(peerIDcompressed[:], data[index:index+33])

			ipB := make([]byte, 16)
			copy(ipB[:], data[index+33:index+33+16])
			peer.IP = ipB
			if peer.IP.To4() != nil { // If IPv4, convert to native 4-byte representation
				peer.IP = peer.IP.To4()
			}

			peer.Port = binary.LittleEndian.Uint16(data[index+49 : index+49+2])
			peer.LastContact = binary.LittleEndian.Uint32(data[index+51 : index+51+4])
			reason := data[index+55]

			var err error
			if peer.PublicKey, err = btcec.ParsePubKey(peerIDcompressed, btcec.S256()); err != nil {
				return nil, 0, false
			}

			peer.NodeID = publicKey2NodeID(peer.PublicKey)

			if reason == 0 { // Peer was returned because it is close to the requested hash
				hash2Peer.Closest = append(hash2Peer.Closest, peer)
			} else if reason == 1 { // Peer stores the data
				hash2Peer.Storing = append(hash2Peer.Storing, peer)
			}

			index += 56
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

		hash := make([]byte, hashSize)
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
		if !bytes.Equal(hash, hashData(fileData)) {
			return nil, read, false
		}

		filesEmbed = append(filesEmbed, EmbeddedFileData{ID: KeyHash{Hash: hash}, Data: fileData})
	}

	return filesEmbed, read, true
}

// ---- message encoding ----

const udpMaxPacketSize = 65507

// isPacketSizeExceed checks if the max packet size would be exceeded with the payload
func isPacketSizeExceed(currentSize int, testSize int) bool {
	return currentSize+testSize > udpMaxPacketSize-packetLengthMin
}

func selfFeatureSupport() (feature byte) {
	// networksMutex not needed here
	if len(networks4) > 0 {
		feature |= 1 << FeatureIPv4Listen
	}
	if len(networks6) > 0 {
		feature |= 1 << FeatureIPv6Listen
	}
	return feature
}

// announcementPacket contains information about a single announcement message
type announcementPacket struct {
	raw      []byte          // The raw packet
	hashes   [][]byte        // List of hashes that are being searched for
	sequence *sequenceExpiry // Sequence
	err      error           // Sending error, if any
}

// msgEncodeAnnouncement encodes an announcement message. It may return multiple messages if the input does not fit into one.
// findPeer is a list of node IDs (blake3 hash of peer ID compressed form)
// findValue is a list of hashes
// files is a list of files stored to inform about
func msgEncodeAnnouncement(sendUA, findSelf bool, findPeer []KeyHash, findValue []KeyHash, files []InfoStore) (packets []*announcementPacket) {
createPacketLoop:
	for {
		packet := &announcementPacket{}
		packets = append(packets, packet)

		raw := make([]byte, 64*1024) // max UDP packet size
		packetSize := announcementPayloadHeaderSize

		raw[0] = byte(ProtocolVersion) // Protocol
		raw[1] = selfFeatureSupport()  // Feature support
		//raw[2] = Actions                                   // Action bit array
		binary.LittleEndian.PutUint32(raw[3:7], BlockchainHeight)
		binary.LittleEndian.PutUint64(raw[7:15], BlockchainVersion)

		// only on initial announcement the User Agent must be provided according to the protocol spec
		if sendUA {
			if len(UserAgent) > 255 {
				UserAgent = UserAgent[:255]
			}
			userAgentB := []byte(UserAgent)

			raw[15] = byte(len(userAgentB))
			copy(raw[announcementPayloadHeaderSize:announcementPayloadHeaderSize+len(userAgentB)], userAgentB)
			packetSize += len(userAgentB)
		}

		// FIND_SELF
		if findSelf {
			raw[2] |= 1 << ActionFindSelf

			packet.hashes = append(packet.hashes, nodeID)
		}

		// FIND_PEER
		if len(findPeer) > 0 {
			// check if there is enough space for at least the header and 1 record
			if isPacketSizeExceed(packetSize, 2+32) {
				packet.raw = raw[:packetSize]
				continue createPacketLoop
			}

			raw[2] |= 1 << ActionFindPeer
			index := packetSize
			packetSize += 2

			for n, find := range findPeer {
				// check if minimum length is available in packet
				if isPacketSizeExceed(packetSize, 32) {
					packet.raw = raw[:packetSize]
					findPeer = findPeer[n:]
					continue createPacketLoop
				}

				binary.LittleEndian.PutUint16(raw[index:index+2], uint16(n+1))
				copy(raw[index+2+32*n:index+2+32*n+32], find.Hash)
				packetSize += 32

				packet.hashes = append(packet.hashes, find.Hash)
			}

			findPeer = nil
		}

		// FIND_VALUE
		if len(findValue) > 0 {
			// check if there is enough space for at least the header and 1 record
			if isPacketSizeExceed(packetSize, 2+32) {
				packet.raw = raw[:packetSize]
				continue createPacketLoop
			}

			raw[2] |= 1 << ActionFindValue
			index := packetSize
			packetSize += 2

			for n, find := range findValue {
				// check if minimum length is available in packet
				if isPacketSizeExceed(packetSize, 32) {
					packet.raw = raw[:packetSize]
					findValue = findValue[n:]
					continue createPacketLoop
				}

				binary.LittleEndian.PutUint16(raw[index:index+2], uint16(n+1))
				copy(raw[index+2+32*n:index+2+32*n+32], find.Hash)
				packetSize += 32

				packet.hashes = append(packet.hashes, find.Hash)
			}

			findValue = nil
		}

		// INFO_STORE
		if len(files) > 0 {
			// check if there is enough space for at least the header and 1 record
			if isPacketSizeExceed(packetSize, 2+41) {
				packet.raw = raw[:packetSize]
				continue createPacketLoop
			}

			raw[2] |= 1 << ActionInfoStore
			index := packetSize
			packetSize += 2

			for n, file := range files {
				// check if minimum length is available in packet
				if isPacketSizeExceed(packetSize, 41) {
					packet.raw = raw[:packetSize]
					files = files[n:]
					continue createPacketLoop
				}

				binary.LittleEndian.PutUint16(raw[index:index+2], uint16(n+1))
				copy(raw[index+2+41*n:index+2+41*n+32], file.ID.Hash)

				binary.LittleEndian.PutUint64(raw[index+2+41*n+32:index+2+41*n+32+8], file.Size)
				raw[index+2+41*n+40] = file.Type

				packetSize += 41
			}

			files = nil
		}

		packet.raw = raw[:packetSize]

		if len(findPeer) == 0 && len(findValue) == 0 && len(files) == 0 {
			return
		}
	}
}

// EmbeddedFileSizeMax is the maximum size of embedded files in response messages. Any file exceeding that must be shared via regular file transfer.
const EmbeddedFileSizeMax = udpMaxPacketSize - packetLengthMin - announcementPayloadHeaderSize - 2 - 35

// msgEncodeResponse encodes a response message
// hash2Peers will be modified.
func msgEncodeResponse(sendUA bool, hash2Peers []Hash2Peer, filesEmbed []EmbeddedFileData, hashesNotFound [][]byte) (packetsRaw [][]byte, err error) {
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
		raw[1] = selfFeatureSupport()  // Feature support
		//raw[2] = Actions                                   // Action bit array
		binary.LittleEndian.PutUint32(raw[3:7], BlockchainHeight)
		binary.LittleEndian.PutUint64(raw[7:15], BlockchainVersion)

		// only on initial response the User Agent must be provided according to the protocol spec
		if sendUA {
			if len(UserAgent) > 255 {
				UserAgent = UserAgent[:255]
			}
			userAgentB := []byte(UserAgent)

			raw[15] = byte(len(userAgentB))
			copy(raw[announcementPayloadHeaderSize:announcementPayloadHeaderSize+len(userAgentB)], userAgentB)
			packetSize += len(userAgentB)
		}

		// 3 count field at raw[index]: count of peer responses, embedded files, and hashes not found
		countIndex := packetSize
		packetSize += 6

		// Encode the peer response data for FIND_SELF, FIND_PEER and FIND_VALUE requests.
		if len(hash2Peers) > 0 {
			for n, hash2Peer := range hash2Peers {
				if isPacketSizeExceed(packetSize, 34+56) { // check if minimum length is available in packet
					packetsRaw = append(packetsRaw, raw[:packetSize])
					hash2Peers = hash2Peers[n:]
					continue createPacketLoop
				}

				index := packetSize
				copy(raw[index:index+32], hash2Peer.ID.Hash)
				count2Index := index + 32

				packetSize += 34
				count2 := uint16(0)

				for m, peer := range hash2Peer.Storing {
					if isPacketSizeExceed(packetSize, 56) { // check if minimum length is available in packet
						packetsRaw = append(packetsRaw, raw[:packetSize])
						hash2Peers = hash2Peers[n:]
						hash2Peer.Storing = hash2Peer.Storing[m:]
						continue createPacketLoop
					}

					index := packetSize
					copy(raw[index:index+33], peer.PublicKey.SerializeCompressed())
					copy(raw[index+33:index+33+16], peer.IP.To16())
					binary.LittleEndian.PutUint16(raw[index+49:index+51], peer.Port)
					binary.LittleEndian.PutUint32(raw[index+51:index+55], peer.LastContact)
					raw[index+55] = 1

					packetSize += 56
					binary.LittleEndian.PutUint16(raw[count2Index+0:count2Index+2], uint16(m+1))
					count2++
				}

				hash2Peer.Storing = nil

				for m, peer := range hash2Peer.Closest {
					if isPacketSizeExceed(packetSize, 56) { // check if minimum length is available in packet
						packetsRaw = append(packetsRaw, raw[:packetSize])
						hash2Peers = hash2Peers[n:]
						hash2Peer.Closest = hash2Peer.Closest[m:]
						continue createPacketLoop
					}

					index := packetSize
					copy(raw[index:index+33], peer.PublicKey.SerializeCompressed())
					copy(raw[index+33:index+33+16], peer.IP.To16())
					binary.LittleEndian.PutUint16(raw[index+49:index+51], peer.Port)
					binary.LittleEndian.PutUint32(raw[index+51:index+55], peer.LastContact)
					raw[index+55] = 0

					packetSize += 56
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

// ---- messages sending ----

// pingConnection sends a ping to the target peer via the specified connection
func (peer *PeerInfo) pingConnection(connection *Connection) {
	err := peer.sendConnection(&PacketRaw{Command: CommandPing, Sequence: peer.msgNewSequence(nil).sequence}, connection)
	connection.LastPingOut = time.Now()

	if (connection.Status == ConnectionActive || connection.Status == ConnectionRedundant) && IsNetworkErrorFatal(err) {
		peer.invalidateActiveConnection(connection)
	}
}

// Chat sends a text message
func (peer *PeerInfo) Chat(text string) {
	peer.send(&PacketRaw{Command: CommandChat, Payload: []byte(text)})
}

// sendAnnouncement sends the announcement message. It acquires a new sequence for each message.
func (peer *PeerInfo) sendAnnouncement(sendUA, findSelf bool, findPeer []KeyHash, findValue []KeyHash, files []InfoStore, sequenceData interface{}) (packets []*announcementPacket) {
	packets = msgEncodeAnnouncement(sendUA, findSelf, findPeer, findValue, files)

	for _, packet := range packets {
		packet.sequence = peer.msgNewSequence(sequenceData)
		packet.err = peer.send(&PacketRaw{Command: CommandAnnouncement, Payload: packet.raw, Sequence: packet.sequence.sequence})
	}

	return
}

// sendResponse sends the response message
func (peer *PeerInfo) sendResponse(sequence uint32, sendUA bool, hash2Peers []Hash2Peer, filesEmbed []EmbeddedFileData, hashesNotFound [][]byte) (err error) {
	packets, err := msgEncodeResponse(sendUA, hash2Peers, filesEmbed, hashesNotFound)

	for _, packet := range packets {
		peer.send(&PacketRaw{Command: CommandResponse, Payload: packet, Sequence: sequence})
	}

	return err
}

// lastContact2Time translates a last contact time in seconds to a timestamp
func lastContact2Time(LastContact uint32) time.Time {
	return time.Now().Add(-time.Second * time.Duration(LastContact))
}
