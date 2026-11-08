/*
File Name:  Message Encoding Response.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner

The Response message (whitepaper § Response). Same header as the Announcement; typed response records follow.
*/

package protocol

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"net"
	"time"
)

// MessageResponse is the decoded response message.
type MessageResponse struct {
	*MessageRaw                        // Underlying raw message
	Protocol          uint8            // Protocol version supported (low 4 bits).
	Features          uint8            // Feature support
	BlockchainHeight  uint64           // Blockchain height
	BlockchainVersion uint64           // Blockchain version
	PortInternal      uint16           // Internal port. Can be used to detect NATs.
	PortExternal      uint16           // External port if known. 0 if not. Can be used for UPnP support.
	UserAgent         string           // User Agent. Format "Software/Version". Required in the initial announcement/bootstrap. UTF-8 encoded. Max length is 255 bytes.
	SequenceLast      bool             // SEQUENCE_LAST: no further response messages follow for the announcement
	Records           []ResponseRecord // Response records in the order received
}

// ResponseRecord is one response record: A_RESPONSE_SELF, A_RESPONSE_PEER, B_RESPONSE_BLOCKCHAIN, B_RESPONSE_FILE or B_RESPONSE_FILE_EMBED.
// Which fields are set depends on the type.
type ResponseRecord struct {
	Type    uint8         // ResponseX
	Key     []byte        // Requested key. nil for A_RESPONSE_SELF.
	Storing []StoringPeer // B_RESPONSE_BLOCKCHAIN, B_RESPONSE_FILE: peers that store the data
	Closest []PeerRecord  // A_RESPONSE_SELF, A_RESPONSE_PEER, B_RESPONSE_BLOCKCHAIN, B_RESPONSE_FILE: peers close to the key (to the sender for A_RESPONSE_SELF)
	IsLast  bool          // Bit 15 of the peer record count: no more results follow for the key. A key with no peer records and IsLast was not found.
	Data    []byte        // B_RESPONSE_FILE_EMBED: the file
}

// StoringPeer is a peer that stores the requested data.
type StoringPeer struct {
	PeerRecord
	BlockchainHeight  uint64 // B_RESPONSE_BLOCKCHAIN: blockchain height stored
	BlockchainVersion uint64 // B_RESPONSE_BLOCKCHAIN: blockchain version stored
}

// PeerRecord informs about a peer.
type PeerRecord struct {
	PublicKey                ed25519.PublicKey // Public Key = Peer ID
	IPv4                     net.IP            // IPv4 address. 0 if not set.
	IPv4Port                 uint16            // Port (actual one used for connection)
	IPv4PortReportedInternal uint16            // Internal port as reported by that peer. This can be used to identify whether the peer is potentially behind a NAT.
	IPv4PortReportedExternal uint16            // External port as reported by that peer. This is used in case of port forwarding (manual or automated).
	IPv6                     net.IP            // IPv6 address. 0 if not set.
	IPv6Port                 uint16            // Port (actual one used for connection)
	IPv6PortReportedInternal uint16            // Internal port as reported by that peer. This can be used to identify whether the peer is potentially behind a NAT.
	IPv6PortReportedExternal uint16            // External port as reported by that peer. This is used in case of port forwarding (manual or automated).
	LastContact              uint32            // Last contact in seconds
	LastContactT             time.Time         // Last contact time translated from seconds
	Features                 uint8             // Feature support. Same as in Announcement/Response message.
}

// Response record types in the Response message. Names as in the whitepaper.
const (
	ResponseASelf       = 0 // A_RESPONSE_SELF: peers close to the sender. Answers A_FIND_SELF.
	ResponseAPeer       = 1 // A_RESPONSE_PEER: peers close to the requested Peer ID. Answers A_FIND_PEER.
	ResponseBBlockchain = 2 // B_RESPONSE_BLOCKCHAIN: peers that store the requested blockchain, and peers close to the key. Answers B_FIND_BLOCKCHAIN.
	ResponseBFile       = 3 // B_RESPONSE_FILE: peers that store the requested file, and peers close to the key. Answers B_FIND_FILE.
	ResponseBFileEmbed  = 4 // B_RESPONSE_FILE_EMBED: the requested file embedded. Answers B_FIND_FILE.
)

// peerRecordSize is the size of a peer record in bytes.
const peerRecordSize = 69

// storingBlockchainRecordSize is the size of a storing peer record in B_RESPONSE_BLOCKCHAIN: peer record + height (8) + version (8).
const storingBlockchainRecordSize = peerRecordSize + 16

// peerCountLastFlag is bit 15 of a peer record count: the last result for the key.
const peerCountLastFlag = 0x8000

// EmbeddedFileSizeMax is the maximum size of a file embedded in a response message (B_RESPONSE_FILE_EMBED). Any file exceeding that must be shared via regular file transfer.
// It leaves room for the header with the maximum User Agent, the record count, the record header and the key.
const EmbeddedFileSizeMax = messagePayloadMax - announcementPayloadHeaderSize - 255 - 1 - recordHeaderSize - HashSize

// DecodeResponse decodes the incoming response message. Returns nil if invalid.
// Records of an unknown type are skipped.
func DecodeResponse(msg *MessageRaw) (result *MessageResponse, err error) {
	header, records, sequenceLast, err := decodeMessageHeader(msg.Payload, "response")
	if err != nil {
		return nil, err
	}

	result = &MessageResponse{
		MessageRaw:        msg,
		Protocol:          header.Protocol,
		Features:          header.Features,
		BlockchainHeight:  header.BlockchainHeight,
		BlockchainVersion: header.BlockchainVersion,
		PortInternal:      header.PortInternal,
		PortExternal:      header.PortExternal,
		UserAgent:         header.UserAgent,
		SequenceLast:      sequenceLast,
	}

	for _, raw := range records {
		record := ResponseRecord{Type: raw.Type}
		data := raw.Data

		switch raw.Type {
		case ResponseASelf, ResponseAPeer, ResponseBBlockchain, ResponseBFile:
			if raw.Type != ResponseASelf {
				if len(data) < HashSize {
					return nil, errors.New("response: record too short for key")
				}
				record.Key = make([]byte, HashSize)
				copy(record.Key, data[:HashSize])
				data = data[HashSize:]
			}

			if len(data) < 2 {
				return nil, errors.New("response: record too short for count")
			}
			countField := binary.LittleEndian.Uint16(data[0:2])
			record.IsLast = countField&peerCountLastFlag > 0
			count := int(countField & 0x7FFF)
			data = data[2:]

			if raw.Type == ResponseBBlockchain || raw.Type == ResponseBFile {
				// count is the storing peers, followed by the count of closest peers
				if len(data) < 2 {
					return nil, errors.New("response: record too short for count")
				}
				countClosest := int(binary.LittleEndian.Uint16(data[0:2]))
				data = data[2:]

				for n := 0; n < count; n++ {
					storing := StoringPeer{}
					size := peerRecordSize
					if raw.Type == ResponseBBlockchain {
						size = storingBlockchainRecordSize
					}
					if len(data) < size {
						return nil, errors.New("response: storing peer record overflow")
					}
					if !decodePeerRecord(data, &storing.PeerRecord) {
						return nil, errors.New("response: invalid peer record")
					}
					if raw.Type == ResponseBBlockchain {
						storing.BlockchainHeight = binary.LittleEndian.Uint64(data[peerRecordSize : peerRecordSize+8])
						storing.BlockchainVersion = binary.LittleEndian.Uint64(data[peerRecordSize+8 : peerRecordSize+16])
					}
					data = data[size:]

					record.Storing = append(record.Storing, storing)
				}

				count = countClosest
			}

			for n := 0; n < count; n++ {
				if len(data) < peerRecordSize {
					return nil, errors.New("response: peer record overflow")
				}
				peer := PeerRecord{}
				if !decodePeerRecord(data, &peer) {
					return nil, errors.New("response: invalid peer record")
				}
				data = data[peerRecordSize:]

				record.Closest = append(record.Closest, peer)
			}

			if len(data) != 0 {
				return nil, errors.New("response: record size mismatch")
			}

		case ResponseBFileEmbed:
			if len(data) < HashSize {
				return nil, errors.New("response: record too short for key")
			}
			record.Key = make([]byte, HashSize)
			copy(record.Key, data[:HashSize])
			record.Data = make([]byte, len(data)-HashSize)
			copy(record.Data, data[HashSize:])

			if !bytes.Equal(record.Key, HashData(record.Data)) {
				return nil, errors.New("response: embedded file hash mismatch")
			}

		default:
			continue
		}

		result.Records = append(result.Records, record)
	}

	return result, nil
}

// decodePeerRecord decodes a single peer record. data must be at least peerRecordSize.
func decodePeerRecord(data []byte, peer *PeerRecord) (valid bool) {
	peer.PublicKey = make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(peer.PublicKey, data[0:32])

	// IPv4
	ipv4B := make([]byte, 4)
	copy(ipv4B, data[32:32+4])
	peer.IPv4 = ipv4B
	peer.IPv4Port = binary.LittleEndian.Uint16(data[36 : 36+2])
	peer.IPv4PortReportedInternal = binary.LittleEndian.Uint16(data[38 : 38+2])
	peer.IPv4PortReportedExternal = binary.LittleEndian.Uint16(data[40 : 40+2])

	// IPv6
	ipv6B := make([]byte, 16)
	copy(ipv6B, data[42:42+16])
	peer.IPv6 = ipv6B
	peer.IPv6Port = binary.LittleEndian.Uint16(data[58 : 58+2])
	peer.IPv6PortReportedInternal = binary.LittleEndian.Uint16(data[60 : 60+2])
	peer.IPv6PortReportedExternal = binary.LittleEndian.Uint16(data[62 : 62+2])

	if peer.IPv6.To4() != nil { // IPv6 address mismatch
		return false
	}

	peer.LastContact = binary.LittleEndian.Uint32(data[64 : 64+4])
	peer.LastContactT = time.Now().Add(-time.Second * time.Duration(peer.LastContact))
	peer.Features = data[68]

	return true
}

// encodePeerRecord encodes a single peer record into raw, which must be at least peerRecordSize.
func encodePeerRecord(raw []byte, peer *PeerRecord) {
	copy(raw[0:0+32], peer.PublicKey)

	// IPv4
	copy(raw[32:32+4], peer.IPv4.To4())
	binary.LittleEndian.PutUint16(raw[36:36+2], peer.IPv4Port)
	binary.LittleEndian.PutUint16(raw[38:38+2], peer.IPv4PortReportedInternal)
	binary.LittleEndian.PutUint16(raw[40:40+2], peer.IPv4PortReportedExternal)

	// IPv6
	copy(raw[42:42+16], peer.IPv6.To16())
	binary.LittleEndian.PutUint16(raw[58:58+2], peer.IPv6Port)
	binary.LittleEndian.PutUint16(raw[60:60+2], peer.IPv6PortReportedInternal)
	binary.LittleEndian.PutUint16(raw[62:62+2], peer.IPv6PortReportedExternal)

	binary.LittleEndian.PutUint32(raw[64:64+4], peer.LastContact)
	raw[68] = peer.Features
}

// encodeResponseRecord encodes the data of a response record.
func encodeResponseRecord(record *ResponseRecord) (data []byte, err error) {
	switch record.Type {
	case ResponseASelf, ResponseAPeer, ResponseBBlockchain, ResponseBFile:
		if record.Type != ResponseASelf {
			if len(record.Key) != HashSize {
				return nil, errors.New("invalid key size")
			}
			data = append(data, record.Key...)
		}

		countField := uint16(len(record.Closest))
		if record.Type == ResponseBBlockchain || record.Type == ResponseBFile {
			countField = uint16(len(record.Storing))
		}
		if record.IsLast {
			countField |= peerCountLastFlag
		}
		data = binary.LittleEndian.AppendUint16(data, countField)

		if record.Type == ResponseBBlockchain || record.Type == ResponseBFile {
			data = binary.LittleEndian.AppendUint16(data, uint16(len(record.Closest)))

			for n := range record.Storing {
				raw := make([]byte, peerRecordSize)
				encodePeerRecord(raw, &record.Storing[n].PeerRecord)
				data = append(data, raw...)

				if record.Type == ResponseBBlockchain {
					data = binary.LittleEndian.AppendUint64(data, record.Storing[n].BlockchainHeight)
					data = binary.LittleEndian.AppendUint64(data, record.Storing[n].BlockchainVersion)
				}
			}
		}

		for n := range record.Closest {
			raw := make([]byte, peerRecordSize)
			encodePeerRecord(raw, &record.Closest[n])
			data = append(data, raw...)
		}

	case ResponseBFileEmbed:
		if len(record.Key) != HashSize {
			return nil, errors.New("invalid key size")
		}
		if len(record.Data) > EmbeddedFileSizeMax {
			return nil, errors.New("embedded file too big")
		}
		data = append(data, record.Key...)
		data = append(data, record.Data...)

	default:
		return nil, errors.New("unknown response record type")
	}

	if len(data) > recordDataSizeMax {
		return nil, errors.New("response record too big")
	}

	return data, nil
}

// EncodeResponse encodes a response message. It returns multiple messages if the records do not fit into one; the last message carries the SEQUENCE_LAST flag.
// A record is never split over messages; the caller limits the peer records per response record.
func EncodeResponse(sendUA bool, records []ResponseRecord, features byte, blockchainHeight, blockchainVersion uint64, userAgent string) (packetsRaw [][]byte, err error) {
	b := newMessageBuilder(sendUA, features, blockchainHeight, blockchainVersion, userAgent)

	for n := range records {
		data, err := encodeResponseRecord(&records[n])
		if err != nil {
			return nil, err
		}

		if len(data) > b.space() {
			b.finish(false)
			b.start()

			if len(data) > b.space() {
				return nil, errors.New("response record too big")
			}
		}

		b.add(records[n].Type, data)
	}

	b.finish(true)

	return b.messages, nil
}

// IsLast checks if the incoming message is the last expected response in this sequence.
func (msg *MessageResponse) IsLast() bool {
	return msg.SequenceLast
}
