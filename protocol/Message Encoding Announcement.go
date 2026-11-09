/*
File Name:  Message Encoding Announcement.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner

The Announcement message (whitepaper § Announcement). The header describes the sender; typed request records follow.
*/

package protocol

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"unicode/utf8"
)

// MessageAnnouncement is the decoded announcement message.
type MessageAnnouncement struct {
	*MessageRaw              // Underlying raw message
	Protocol          uint8  // Protocol version supported (low 4 bits).
	Features          uint8  // Feature support
	BlockchainHeight  uint64 // Blockchain height
	BlockchainVersion uint64 // Blockchain version
	PortInternal      uint16 // Internal port. Can be used to detect NATs.
	PortExternal      uint16 // External port if known. 0 if not. Can be used for UPnP support.
	UserAgent         string // User Agent. Format "Software/Version". Required in the initial announcement/bootstrap. UTF-8 encoded. Max length is 255 bytes.
	Requests                 // Request records
}

// Requests are the request records carried in an announcement. Records of the same type are merged.
type Requests struct {
	FindSelf        bool              // A_FIND_SELF: request closest peers to the sender
	FindPeer        [][]byte          // A_FIND_PEER: Peer IDs to request closest peers for
	FindBlockchain  [][]byte          // B_FIND_BLOCKCHAIN: Peer IDs to request storing peers for
	FindFile        [][]byte          // B_FIND_FILE: file hashes to request storing peers for
	FindFileEmbed   [][]byte          // B_FIND_FILE_EMBED: file hashes to request storing peers for, or the file itself if it fits into the packet
	StoreBlockchain []BlockchainStore // B_STORE_BLOCKCHAIN: blockchains the sender stores
	StoreFile       []FileStore       // B_STORE_FILE: files the sender stores
}

// IsEmpty is true if there are no requests.
func (r *Requests) IsEmpty() bool {
	return r == nil || !r.FindSelf && len(r.FindPeer) == 0 && len(r.FindBlockchain) == 0 && len(r.FindFile) == 0 && len(r.FindFileEmbed) == 0 && len(r.StoreBlockchain) == 0 && len(r.StoreFile) == 0
}

// BlockchainStore is one item of B_STORE_BLOCKCHAIN: a blockchain the sender stores.
type BlockchainStore struct {
	PublicKey ed25519.PublicKey // Peer ID of the blockchain
	Height    uint64            // Blockchain height stored
	Version   uint64            // Blockchain version stored
}

// FileStore is one item of B_STORE_FILE: a file the sender stores.
type FileStore struct {
	Hash []byte // Blake3 hash of the file
	Size uint64 // Size of the file
	Type uint8  // Type of the file: 0 = File, 1 = Header file containing list of parts
}

// Features are sent as bit array in the Announcement message.
const (
	FeatureIPv4Listen = 0 // Sender listens on IPv4
	FeatureIPv6Listen = 1 // Sender listens on IPv6
	FeatureFirewall   = 2 // Sender indicates a potential firewall. This informs uncontacted peers that a Traverse message might be required to establish a connection.
)

// Request record types in the Announcement message. Names as in the whitepaper.
const (
	RequestAFindSelf        = 0 // A_FIND_SELF: request closest peers to the sender (DHT A)
	RequestAFindPeer        = 1 // A_FIND_PEER: request closest peers to each Peer ID (DHT A)
	RequestBFindBlockchain  = 2 // B_FIND_BLOCKCHAIN: request peers that store the blockchain of each Peer ID (DHT B)
	RequestBFindFile        = 3 // B_FIND_FILE: request peers that store each file (DHT B)
	RequestBFindFileEmbed   = 4 // B_FIND_FILE_EMBED: as B_FIND_FILE, but the file itself if the receiver stores it and it fits into the packet (DHT B)
	RequestBStoreBlockchain = 5 // B_STORE_BLOCKCHAIN: sender indicates storing the listed blockchains (DHT B)
	RequestBStoreFile       = 6 // B_STORE_FILE: sender indicates storing the listed files (DHT B)
)

// Sizes of the fixed-size list items in request records.
const (
	blockchainStoreSize = 48 // B_STORE_BLOCKCHAIN item: Peer ID (32) + height (8) + version (8)
	fileStoreSize       = 41 // B_STORE_FILE item: hash (32) + size (8) + type (1)
)

// announcementPayloadHeaderSize is the size of the Announcement and Response payload header up to the User Agent.
const announcementPayloadHeaderSize = 23

// recordHeaderSize is the size of the record header: type (1) + size of data (2).
const recordHeaderSize = 3

// recordDataSizeMax is the maximum size of the data of one record (2-byte size field).
const recordDataSizeMax = 65535

// recordCountMax is the maximum count of records per message (7-bit count).
const recordCountMax = 127

// sequenceLastFlag is bit 7 of the record count byte. In a response it indicates that no further response messages follow for the announcement.
const sequenceLastFlag = 0x80

// messagePayloadMax is the maximum payload size of one message.
const messagePayloadMax = udpMaxPacketSize - PacketLengthMin

// messageHeader are the fields shared by the Announcement and Response messages.
type messageHeader struct {
	Protocol          uint8
	Features          uint8
	BlockchainHeight  uint64
	BlockchainVersion uint64
	PortInternal      uint16
	PortExternal      uint16
	UserAgent         string
}

// rawRecord is a record before its data is interpreted.
type rawRecord struct {
	Type uint8
	Data []byte
}

// decodeMessageHeader decodes the header and the records of an Announcement or Response message.
func decodeMessageHeader(payload []byte, name string) (header messageHeader, records []rawRecord, sequenceLast bool, err error) {
	if len(payload) < announcementPayloadHeaderSize+1 {
		return header, nil, false, errors.New(name + ": invalid minimum length")
	}

	header.Protocol = payload[0] & 0x0F // Protocol version support is stored in the first 4 bits
	header.Features = payload[1]
	header.BlockchainHeight = binary.LittleEndian.Uint64(payload[2 : 2+8])
	header.BlockchainVersion = binary.LittleEndian.Uint64(payload[10 : 10+8])
	header.PortInternal = binary.LittleEndian.Uint16(payload[18 : 18+2])
	header.PortExternal = binary.LittleEndian.Uint16(payload[20 : 20+2])

	userAgentLength := int(payload[22])
	read := announcementPayloadHeaderSize

	if userAgentLength > 0 {
		if read+userAgentLength+1 > len(payload) {
			return header, nil, false, errors.New(name + ": user agent overflow")
		}

		userAgentB := payload[read : read+userAgentLength]
		if !utf8.Valid(userAgentB) {
			return header, nil, false, errors.New(name + ": user agent invalid encoding")
		}

		header.UserAgent = string(userAgentB)
		read += userAgentLength
	}

	count := int(payload[read] & 0x7F)
	sequenceLast = payload[read]&sequenceLastFlag > 0
	read++

	for n := 0; n < count; n++ {
		if read+recordHeaderSize > len(payload) {
			return header, nil, false, errors.New(name + ": record header overflow")
		}

		recordType := payload[read]
		size := int(binary.LittleEndian.Uint16(payload[read+1 : read+1+2]))
		read += recordHeaderSize

		if read+size > len(payload) {
			return header, nil, false, errors.New(name + ": record data overflow")
		}

		records = append(records, rawRecord{Type: recordType, Data: payload[read : read+size]})
		read += size
	}

	// Extra data after the records is accepted in case future features append additional data.

	return header, records, sequenceLast, nil
}

// DecodeAnnouncement decodes the incoming announcement message. Returns nil if invalid.
// Records of an unknown type are skipped.
func DecodeAnnouncement(msg *MessageRaw) (result *MessageAnnouncement, err error) {
	header, records, _, err := decodeMessageHeader(msg.Payload, "announcement")
	if err != nil {
		return nil, err
	}

	result = &MessageAnnouncement{
		MessageRaw:        msg,
		Protocol:          header.Protocol,
		Features:          header.Features,
		BlockchainHeight:  header.BlockchainHeight,
		BlockchainVersion: header.BlockchainVersion,
		PortInternal:      header.PortInternal,
		PortExternal:      header.PortExternal,
		UserAgent:         header.UserAgent,
	}

	for _, record := range records {
		switch record.Type {
		case RequestAFindSelf:
			result.FindSelf = true

		case RequestAFindPeer, RequestBFindBlockchain, RequestBFindFile, RequestBFindFileEmbed:
			keys, valid := decodeKeyList(record.Data)
			if !valid {
				return nil, errors.New("announcement: invalid key list")
			}

			switch record.Type {
			case RequestAFindPeer:
				result.FindPeer = append(result.FindPeer, keys...)
			case RequestBFindBlockchain:
				result.FindBlockchain = append(result.FindBlockchain, keys...)
			case RequestBFindFile:
				result.FindFile = append(result.FindFile, keys...)
			case RequestBFindFileEmbed:
				result.FindFileEmbed = append(result.FindFileEmbed, keys...)
			}

		case RequestBStoreBlockchain:
			if len(record.Data)%blockchainStoreSize != 0 {
				return nil, errors.New("announcement: invalid B_STORE_BLOCKCHAIN list")
			}

			for data := record.Data; len(data) > 0; data = data[blockchainStoreSize:] {
				item := BlockchainStore{PublicKey: make(ed25519.PublicKey, ed25519.PublicKeySize)}
				copy(item.PublicKey, data[0:32])
				item.Height = binary.LittleEndian.Uint64(data[32 : 32+8])
				item.Version = binary.LittleEndian.Uint64(data[40 : 40+8])

				result.StoreBlockchain = append(result.StoreBlockchain, item)
			}

		case RequestBStoreFile:
			if len(record.Data)%fileStoreSize != 0 {
				return nil, errors.New("announcement: invalid B_STORE_FILE list")
			}

			for data := record.Data; len(data) > 0; data = data[fileStoreSize:] {
				item := FileStore{Hash: make([]byte, HashSize)}
				copy(item.Hash, data[0:32])
				item.Size = binary.LittleEndian.Uint64(data[32 : 32+8])
				item.Type = data[40]

				result.StoreFile = append(result.StoreFile, item)
			}
		}
	}

	return result, nil
}

// decodeKeyList decodes a list of 32-byte keys. The count follows from the size.
func decodeKeyList(data []byte) (keys [][]byte, valid bool) {
	if len(data)%HashSize != 0 {
		return nil, false
	}

	for ; len(data) > 0; data = data[HashSize:] {
		key := make([]byte, HashSize)
		copy(key, data[:HashSize])
		keys = append(keys, key)
	}

	return keys, true
}

// messageBuilder assembles Announcement and Response messages. Records that do not fit into the current message start a new one.
type messageBuilder struct {
	sendUA            bool
	features          byte
	blockchainHeight  uint64
	blockchainVersion uint64
	userAgent         string

	raw        []byte   // current message
	size       int      // size of the current message
	countIndex int      // index of the record count byte in the current message
	count      int      // records in the current message
	messages   [][]byte // finished messages
}

func newMessageBuilder(sendUA bool, features byte, blockchainHeight, blockchainVersion uint64, userAgent string) (b *messageBuilder) {
	b = &messageBuilder{sendUA: sendUA, features: features, blockchainHeight: blockchainHeight, blockchainVersion: blockchainVersion, userAgent: userAgent}
	b.start()
	return b
}

// start begins a new message with the header.
func (b *messageBuilder) start() {
	b.raw = make([]byte, messagePayloadMax)
	b.raw[0] = byte(ProtocolVersion)
	b.raw[1] = b.features
	binary.LittleEndian.PutUint64(b.raw[2:2+8], b.blockchainHeight)
	binary.LittleEndian.PutUint64(b.raw[10:10+8], b.blockchainVersion)
	b.size = announcementPayloadHeaderSize

	// only on the initial announcement/response the User Agent must be provided according to the protocol spec
	if b.sendUA {
		userAgentB := []byte(b.userAgent)
		if len(userAgentB) > 255 {
			userAgentB = userAgentB[:255]
		}

		b.raw[22] = byte(len(userAgentB))
		copy(b.raw[b.size:b.size+len(userAgentB)], userAgentB)
		b.size += len(userAgentB)
	}

	b.countIndex = b.size
	b.size++
	b.count = 0
}

// space returns the bytes available for the data of one more record in the current message.
func (b *messageBuilder) space() int {
	space := len(b.raw) - b.size - recordHeaderSize
	if space > recordDataSizeMax {
		space = recordDataSizeMax
	}
	return space
}

// add appends a record. The data must fit (see space).
func (b *messageBuilder) add(recordType uint8, data []byte) {
	if b.count == recordCountMax {
		b.finish(false)
		b.start()
	}

	b.raw[b.size] = recordType
	binary.LittleEndian.PutUint16(b.raw[b.size+1:b.size+3], uint16(len(data)))
	copy(b.raw[b.size+recordHeaderSize:], data)
	b.size += recordHeaderSize + len(data)
	b.count++
}

// addList appends a list of fixed-size items as records of the given type, split over records and messages as needed.
func (b *messageBuilder) addList(recordType uint8, items [][]byte, itemSize int) {
	for len(items) > 0 {
		n := b.space() / itemSize
		if n == 0 {
			b.finish(false)
			b.start()
			continue
		}
		if n > len(items) {
			n = len(items)
		}

		data := make([]byte, 0, n*itemSize)
		for _, item := range items[:n] {
			data = append(data, item...)
		}

		b.add(recordType, data)
		items = items[n:]
	}
}

// finish completes the current message. last sets the SEQUENCE_LAST flag.
func (b *messageBuilder) finish(last bool) {
	b.raw[b.countIndex] = byte(b.count)
	if last {
		b.raw[b.countIndex] |= sequenceLastFlag
	}

	b.messages = append(b.messages, b.raw[:b.size])
}

// EncodeAnnouncement encodes an announcement message. It returns multiple messages if the requests do not fit into one.
// requests may be nil for an announcement of self without requests.
func EncodeAnnouncement(sendUA bool, requests *Requests, features byte, blockchainHeight, blockchainVersion uint64, userAgent string) (packetsRaw [][]byte) {
	b := newMessageBuilder(sendUA, features, blockchainHeight, blockchainVersion, userAgent)

	if requests != nil {
		if requests.FindSelf {
			b.add(RequestAFindSelf, nil)
		}

		b.addList(RequestAFindPeer, requests.FindPeer, HashSize)
		b.addList(RequestBFindBlockchain, requests.FindBlockchain, HashSize)
		b.addList(RequestBFindFile, requests.FindFile, HashSize)
		b.addList(RequestBFindFileEmbed, requests.FindFileEmbed, HashSize)

		var items [][]byte
		for _, item := range requests.StoreBlockchain {
			data := make([]byte, blockchainStoreSize)
			copy(data[0:32], item.PublicKey)
			binary.LittleEndian.PutUint64(data[32:32+8], item.Height)
			binary.LittleEndian.PutUint64(data[40:40+8], item.Version)
			items = append(items, data)
		}
		b.addList(RequestBStoreBlockchain, items, blockchainStoreSize)

		items = nil
		for _, item := range requests.StoreFile {
			data := make([]byte, fileStoreSize)
			copy(data[0:32], item.Hash)
			binary.LittleEndian.PutUint64(data[32:32+8], item.Size)
			data[40] = item.Type
			items = append(items, data)
		}
		b.addList(RequestBStoreFile, items, fileStoreSize)
	}

	b.finish(false) // SEQUENCE_LAST is 0 in an announcement

	return b.messages
}
