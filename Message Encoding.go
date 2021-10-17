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

	"github.com/PeernetOfficial/core/protocol"
	"github.com/btcsuite/btcd/btcec"
)

// ProtocolVersion is the current protocol version
const ProtocolVersion = 0

// UserAgent should be set by the caller
var UserAgent = "Peernet Core/0.1"

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
	protocol.PacketRaw
	SenderPublicKey *btcec.PublicKey // Sender Public Key, ECDSA (secp256k1) 257-bit
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
	PortInternal      uint16      // Internal port. Can be used to detect NATs.
	PortExternal      uint16      // External port if known. 0 if not. Can be used for UPnP support.
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

// MessageTraverse is the decoded traverse message.
// It is sent by an original sender to a relay, to a final receiver (targert peer).
type MessageTraverse struct {
	*MessageRaw                               // Underlying raw message.
	TargetPeer               *btcec.PublicKey // End receiver peer ID.
	AuthorizedRelayPeer      *btcec.PublicKey // Peer ID that is authorized to relay this message to the end receiver.
	Expires                  time.Time        // Expiration time when this forwarded message becomes invalid.
	EmbeddedPacketRaw        []byte           // Embedded packet.
	SignerPublicKey          *btcec.PublicKey // Public key that signed this message, ECDSA (secp256k1) 257-bit
	IPv4                     net.IP           // IPv4 address of the original sender. Set by authorized relay. 0 if not set.
	PortIPv4                 uint16           // Port (actual one used for connection) of the original sender. Set by authorized relay.
	PortIPv4ReportedExternal uint16           // External port as reported by the original sender. This is used in case of port forwarding (manual or automated).
	IPv6                     net.IP           // IPv6 address of the original sender. Set by authorized relay. 0 if not set.
	PortIPv6                 uint16           // Port (actual one used for connection) of the original sender. Set by authorized relay.
	PortIPv6ReportedExternal uint16           // External port as reported by the original sender. This is used in case of port forwarding (manual or automated).
}

// ---- message decoding ----

// Minimum length of Announcement payload header without User Agent
const announcementPayloadHeaderSize = 20

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
	result.PortInternal = binary.LittleEndian.Uint16(msg.Payload[15:17])
	result.PortExternal = binary.LittleEndian.Uint16(msg.Payload[17:19])

	userAgentLength := int(msg.Payload[19])
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
			hash := make([]byte, hashSize)
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

		hash := make([]byte, hashSize)
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

			peer.NodeID = protocol.PublicKey2NodeID(peer.PublicKey)

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
		if !bytes.Equal(hash, protocol.HashData(fileData)) {
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
	return currentSize+testSize > udpMaxPacketSize-protocol.PacketLengthMin
}

func FeatureSupport() (feature byte) {
	if countListen4 > 0 {
		feature |= 1 << FeatureIPv4Listen
	}
	if countListen6 > 0 {
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
		raw[1] = FeatureSupport()      // Feature support
		//raw[2] = Actions                                   // Action bit array

		_, blockchainHeight, blockchainVersion := UserBlockchain.Header()
		binary.LittleEndian.PutUint32(raw[3:7], uint32(blockchainHeight))
		binary.LittleEndian.PutUint64(raw[7:15], blockchainVersion)

		// only on initial announcement the User Agent must be provided according to the protocol spec
		if sendUA {
			userAgentB := []byte(UserAgent)
			if len(userAgentB) > 255 {
				userAgentB = userAgentB[:255]
			}

			raw[19] = byte(len(userAgentB))
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
const EmbeddedFileSizeMax = udpMaxPacketSize - protocol.PacketLengthMin - announcementPayloadHeaderSize - 2 - 35

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
		raw[1] = FeatureSupport()      // Feature support
		//raw[2] = Actions                                   // Action bit array

		_, blockchainHeight, blockchainVersion := UserBlockchain.Header()
		binary.LittleEndian.PutUint32(raw[3:7], uint32(blockchainHeight))
		binary.LittleEndian.PutUint64(raw[7:15], blockchainVersion)

		// only on initial response the User Agent must be provided according to the protocol spec
		if sendUA {
			userAgentB := []byte(UserAgent)
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

// ---- Traverse ----

const traversePayloadHeaderSize = 76 + 65 + 28

// msgDecodeTraverse decodes a traverse message.
// It does not verify if the receiver is authorized to read or forward this message.
// It validates the signature, but does not validate the signer.
func msgDecodeTraverse(msg *MessageRaw) (result *MessageTraverse, err error) {
	result = &MessageTraverse{
		MessageRaw: msg,
	}

	if len(msg.Payload) < traversePayloadHeaderSize {
		return nil, errors.New("traverse: invalid minimum length")
	}

	targetPeerIDcompressed := msg.Payload[0:33]
	authorizedRelayPeerIDcompressed := msg.Payload[33:66]

	if result.TargetPeer, err = btcec.ParsePubKey(targetPeerIDcompressed, btcec.S256()); err != nil {
		return nil, err
	}
	if result.AuthorizedRelayPeer, err = btcec.ParsePubKey(authorizedRelayPeerIDcompressed, btcec.S256()); err != nil {
		return nil, err
	}

	// receiver and target must not be the same
	if result.TargetPeer.IsEqual(result.AuthorizedRelayPeer) {
		return nil, errors.New("traverse: target and relay invalid")
	}

	expires64 := binary.LittleEndian.Uint64(msg.Payload[66 : 66+8])
	result.Expires = time.Unix(int64(expires64), 0)

	sizePacketEmbed := binary.LittleEndian.Uint16(msg.Payload[74 : 74+2])
	if int(sizePacketEmbed) != len(msg.Payload)-traversePayloadHeaderSize {
		return nil, errors.New("traverse: size embedded packet mismatch")
	}

	result.EmbeddedPacketRaw = msg.Payload[76 : 76+sizePacketEmbed]

	signature := msg.Payload[76+sizePacketEmbed : 76+sizePacketEmbed+65]

	result.SignerPublicKey, _, err = btcec.RecoverCompact(btcec.S256(), signature, protocol.HashData(msg.Payload[:76+sizePacketEmbed]))
	if err != nil {
		return nil, err
	}

	// IPv4
	ipv4B := make([]byte, 4)
	copy(ipv4B[:], msg.Payload[76+sizePacketEmbed+65:76+sizePacketEmbed+65+4])

	result.IPv4 = ipv4B
	result.PortIPv4 = binary.LittleEndian.Uint16(msg.Payload[76+sizePacketEmbed+65+4 : 76+sizePacketEmbed+65+4+2])
	result.PortIPv4ReportedExternal = binary.LittleEndian.Uint16(msg.Payload[76+sizePacketEmbed+65+6 : 76+sizePacketEmbed+65+6+2])

	// IPv6
	ipv6B := make([]byte, 16)
	copy(ipv6B[:], msg.Payload[76+sizePacketEmbed+65+8:76+sizePacketEmbed+65+8+16])

	result.IPv6 = ipv6B
	result.PortIPv6 = binary.LittleEndian.Uint16(msg.Payload[76+sizePacketEmbed+65+24 : 76+sizePacketEmbed+65+24+2])
	result.PortIPv6ReportedExternal = binary.LittleEndian.Uint16(msg.Payload[76+sizePacketEmbed+65+26 : 76+sizePacketEmbed+65+26+2])

	// TODO: Validate IPv4 and IPv6. Only external ones allowed.
	if result.IPv6.To4() != nil {
		return nil, errors.New("traverse: ipv6 address mismatch")
	}

	return result, nil
}

// msgEncodeTraverse encodes a traverse message
func msgEncodeTraverse(senderPrivateKey *btcec.PrivateKey, embeddedPacketRaw []byte, receiverEnd *btcec.PublicKey, relayPeer *btcec.PublicKey) (packetRaw []byte, err error) {
	sizePacketEmbed := len(embeddedPacketRaw)
	if isPacketSizeExceed(traversePayloadHeaderSize, sizePacketEmbed) {
		return nil, errors.New("traverse encode: embedded packet too big")
	}

	raw := make([]byte, traversePayloadHeaderSize+sizePacketEmbed)

	targetPeerID := receiverEnd.SerializeCompressed()
	copy(raw[0:33], targetPeerID)
	authorizedRelayPeerID := relayPeer.SerializeCompressed()
	copy(raw[33:66], authorizedRelayPeerID)

	expires64 := time.Now().Add(time.Hour).UTC().Unix()
	binary.LittleEndian.PutUint64(raw[66:66+8], uint64(expires64))

	binary.LittleEndian.PutUint16(raw[74:74+2], uint16(sizePacketEmbed))
	copy(raw[76:76+sizePacketEmbed], embeddedPacketRaw)

	// add signature
	signature, err := btcec.SignCompact(btcec.S256(), senderPrivateKey, protocol.HashData(raw[:76+sizePacketEmbed]), true)
	if err != nil {
		return nil, err
	}
	copy(raw[76+sizePacketEmbed:76+sizePacketEmbed+65], signature)

	// IP and ports are to be filled by authorized relay peer

	return raw, nil
}

// msgEncodeTraverseSetAddress sets the IP and Port
func msgEncodeTraverseSetAddress(raw []byte, IPv4 net.IP, PortIPv4, PortIPv4ReportedExternal uint16, IPv6 net.IP, PortIPv6, PortIPv6ReportedExternal uint16) (err error) {
	if isPacketSizeExceed(len(raw), 0) {
		return errors.New("traverse encode 2: embedded packet too big")
	} else if len(raw) < traversePayloadHeaderSize {
		return errors.New("traverse encode 2: invalid packet")
	}

	sizePacketEmbed := binary.LittleEndian.Uint16(raw[74 : 74+2])
	if int(sizePacketEmbed) != len(raw)-traversePayloadHeaderSize {
		return errors.New("traverse encode 2: size embedded packet mismatch")
	}

	// IPv4
	if IPv4 != nil && IsIPv4(IPv4) {
		copy(raw[76+sizePacketEmbed+65:76+sizePacketEmbed+65+4], IPv4.To4())
		binary.LittleEndian.PutUint16(raw[76+sizePacketEmbed+65+4:76+sizePacketEmbed+65+4+2], PortIPv4)
		binary.LittleEndian.PutUint16(raw[76+sizePacketEmbed+65+6:76+sizePacketEmbed+65+6+2], PortIPv4ReportedExternal)
	}

	// IPv6
	if IPv6 != nil && IsIPv6(IPv6) {
		copy(raw[76+sizePacketEmbed+65+8:76+sizePacketEmbed+65+8+16], IPv6.To16())
		binary.LittleEndian.PutUint16(raw[76+sizePacketEmbed+65+24:76+sizePacketEmbed+65+24+2], PortIPv6)
		binary.LittleEndian.PutUint16(raw[76+sizePacketEmbed+65+26:76+sizePacketEmbed+65+26+2], PortIPv6ReportedExternal)
	}

	return nil
}
