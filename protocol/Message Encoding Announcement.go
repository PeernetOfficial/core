/*
File Name:  Message Encoding Announcement.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package protocol

import (
	"encoding/binary"
	"errors"
	"unicode/utf8"
)

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

// Features are sent as bit array in the Announcement message.
const (
	FeatureIPv4Listen = 0 // Sender listens on IPv4
	FeatureIPv6Listen = 1 // Sender listens on IPv6
)

// Actions between peers, sent via Announcement message. They correspond to the bit array index.
const (
	ActionFindSelf  = 0 // FIND_SELF Request closest neighbors to self
	ActionFindPeer  = 1 // FIND_PEER Request closest neighbors to target peer
	ActionFindValue = 2 // FIND_VALUE Request data or closest peers
	ActionInfoStore = 3 // INFO_STORE Sender indicates storing provided data
)

// Minimum length of Announcement payload header without User Agent
const announcementPayloadHeaderSize = 20

// DecodeAnnouncement decodes the incoming announcement message. Returns nil if invalid.
func DecodeAnnouncement(msg *MessageRaw) (result *MessageAnnouncement, err error) {
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
		files, _, valid := decodeInfoStore(data)
		if !valid {
			return nil, errors.New("announcement: INFO_STORE invalid data")
		}

		// commented out because never used
		//data = data[read:]
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
	if len(data) < 2+HashSize { // minimum length
		return nil, 0, false
	}

	count := binary.LittleEndian.Uint16(data[0:2])

	if read = 2 + int(count)*HashSize; len(data) < read {
		return nil, 0, false
	}

	for n := 0; n < int(count); n++ {
		key := make([]byte, HashSize)
		copy(key, data[2+n*HashSize:2+n*HashSize+HashSize])
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
		file.ID.Hash = make([]byte, HashSize)
		copy(file.ID.Hash, data[2+n*41:2+n*41+HashSize])
		file.Size = binary.LittleEndian.Uint64(data[2+n*41+32 : 2+n*41+32+8])
		file.Type = data[2+n*41+40]

		files = append(files, file)
	}

	return files, read, true
}

// EncodeAnnouncement encodes an announcement message. It may return multiple messages if the input does not fit into one.
// findPeer is a list of node IDs (blake3 hash of peer ID compressed form)
// findValue is a list of hashes
// files is a list of files stored to inform about
func EncodeAnnouncement(sendUA, findSelf bool, findPeer []KeyHash, findValue []KeyHash, files []InfoStore, features byte, blockchainHeight, blockchainVersion uint64, userAgent string) (packetsRaw [][]byte) {
createPacketLoop:
	for {
		raw := make([]byte, 64*1024) // max UDP packet size
		packetSize := announcementPayloadHeaderSize

		raw[0] = byte(ProtocolVersion) // Protocol
		raw[1] = features              // Feature support
		//raw[2] = Actions                                   // Action bit array

		binary.LittleEndian.PutUint32(raw[3:7], uint32(blockchainHeight))
		binary.LittleEndian.PutUint64(raw[7:15], blockchainVersion)

		// only on initial announcement the User Agent must be provided according to the protocol spec
		if sendUA {
			userAgentB := []byte(userAgent)
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
		}

		// FIND_PEER
		if len(findPeer) > 0 {
			// check if there is enough space for at least the header and 1 record
			if isPacketSizeExceed(packetSize, 2+32) {
				packetsRaw = append(packetsRaw, raw[:packetSize])
				continue createPacketLoop
			}

			raw[2] |= 1 << ActionFindPeer
			index := packetSize
			packetSize += 2

			for n, find := range findPeer {
				// check if minimum length is available in packet
				if isPacketSizeExceed(packetSize, 32) {
					packetsRaw = append(packetsRaw, raw[:packetSize])
					findPeer = findPeer[n:]
					continue createPacketLoop
				}

				binary.LittleEndian.PutUint16(raw[index:index+2], uint16(n+1))
				copy(raw[index+2+32*n:index+2+32*n+32], find.Hash)
				packetSize += 32
			}

			findPeer = nil
		}

		// FIND_VALUE
		if len(findValue) > 0 {
			// check if there is enough space for at least the header and 1 record
			if isPacketSizeExceed(packetSize, 2+32) {
				packetsRaw = append(packetsRaw, raw[:packetSize])
				continue createPacketLoop
			}

			raw[2] |= 1 << ActionFindValue
			index := packetSize
			packetSize += 2

			for n, find := range findValue {
				// check if minimum length is available in packet
				if isPacketSizeExceed(packetSize, 32) {
					packetsRaw = append(packetsRaw, raw[:packetSize])
					findValue = findValue[n:]
					continue createPacketLoop
				}

				binary.LittleEndian.PutUint16(raw[index:index+2], uint16(n+1))
				copy(raw[index+2+32*n:index+2+32*n+32], find.Hash)
				packetSize += 32
			}

			findValue = nil
		}

		// INFO_STORE
		if len(files) > 0 {
			// check if there is enough space for at least the header and 1 record
			if isPacketSizeExceed(packetSize, 2+41) {
				packetsRaw = append(packetsRaw, raw[:packetSize])
				continue createPacketLoop
			}

			raw[2] |= 1 << ActionInfoStore
			index := packetSize
			packetSize += 2

			for n, file := range files {
				// check if minimum length is available in packet
				if isPacketSizeExceed(packetSize, 41) {
					packetsRaw = append(packetsRaw, raw[:packetSize])
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

		packetsRaw = append(packetsRaw, raw[:packetSize])

		if len(findPeer) == 0 && len(findValue) == 0 && len(files) == 0 {
			return
		}
	}
}
