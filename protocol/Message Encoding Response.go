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
	IsLast  bool          // Bit 7 of the first peer record count: no more results follow for the key. A key with no peer records and IsLast was not found.
	Data    []byte        // B_RESPONSE_FILE_EMBED: the file
}

// StoringPeer is a peer that stores the requested data.
type StoringPeer struct {
	PeerRecord
	BlockchainHeight  uint64 // B_RESPONSE_BLOCKCHAIN: blockchain height stored
	BlockchainVersion uint64 // B_RESPONSE_BLOCKCHAIN: blockchain version stored
}

// PeerRecord informs about a peer. It carries up to 3 addresses per family, in no particular order.
type PeerRecord struct {
	PublicKey ed25519.PublicKey   // Public Key = Peer ID
	Features  uint8               // Feature support. Same as in Announcement/Response message.
	IPv4      []PeerRecordAddress // IPv4 addresses
	IPv6      []PeerRecordAddress // IPv6 addresses
}

// PeerRecordAddress is one address of a peer in a peer record.
type PeerRecordAddress struct {
	IP                   net.IP // IP address
	Port                 uint16 // Port (actual one used for connection)
	PortReportedInternal uint16 // Internal port as reported by that peer. This can be used to identify whether the peer is potentially behind a NAT.
	PortReportedExternal uint16 // External port as reported by that peer. This is used in case of port forwarding (manual or automated). 0 if not known.
}

// Response record types in the Response message. Names as in the whitepaper.
const (
	ResponseASelf       = 0 // A_RESPONSE_SELF: peers close to the sender. Answers A_FIND_SELF.
	ResponseAPeer       = 1 // A_RESPONSE_PEER: peers close to the requested Peer ID. Answers A_FIND_PEER.
	ResponseBBlockchain = 2 // B_RESPONSE_BLOCKCHAIN: peers that store the requested blockchain, and peers close to the key. Answers B_FIND_BLOCKCHAIN.
	ResponseBFile       = 3 // B_RESPONSE_FILE: peers that store the requested file, and peers close to the key. Answers B_FIND_FILE and B_FIND_FILE_EMBED.
	ResponseBFileEmbed  = 4 // B_RESPONSE_FILE_EMBED: the requested file embedded. Answers B_FIND_FILE_EMBED.
)

// peerRecordHeaderSize is the size of a peer record without its addresses: Peer ID (32), features (1), address counts (1).
const peerRecordHeaderSize = 34

// Size of one address in a peer record: IP address (4 or 16), port (2), internal port (2), external port (2).
const (
	peerRecordAddressSizeIPv4 = net.IPv4len + 6
	peerRecordAddressSizeIPv6 = net.IPv6len + 6
)

// PeerRecordAddressesMax is the maximum count of addresses per family in a peer record. Each count takes 2 bits of the address counts.
const PeerRecordAddressesMax = 3

// storingBlockchainInfoSize is the size of the data that follows the peer record of a storing peer in B_RESPONSE_BLOCKCHAIN: height (8) + version (8).
const storingBlockchainInfoSize = 16

// peerCountLastFlag is bit 7 of the first peer record count: the last result for the key.
const peerCountLastFlag = 0x80

// peerCountMax is the maximum count of peer records in one list of a response record (7 bits).
const peerCountMax = 0x7F

// Size returns the encoded size of the peer record in bytes. It follows from the count of addresses.
func (peer *PeerRecord) Size() int {
	return peerRecordHeaderSize + len(peer.IPv4)*peerRecordAddressSizeIPv4 + len(peer.IPv6)*peerRecordAddressSizeIPv6
}

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

			if len(data) < 1 {
				return nil, errors.New("response: record too short for count")
			}
			record.IsLast = data[0]&peerCountLastFlag > 0
			count := int(data[0] & peerCountMax)
			data = data[1:]

			if raw.Type == ResponseBBlockchain || raw.Type == ResponseBFile {
				// count is the storing peers, followed by the count of closest peers
				if len(data) < 1 {
					return nil, errors.New("response: record too short for count")
				}
				countClosest := int(data[0] & peerCountMax)
				data = data[1:]

				for n := 0; n < count; n++ {
					storing := StoringPeer{}
					size, err := decodePeerRecord(data, &storing.PeerRecord)
					if err != nil {
						return nil, err
					}
					data = data[size:]

					if raw.Type == ResponseBBlockchain {
						if len(data) < storingBlockchainInfoSize {
							return nil, errors.New("response: peer record overflow")
						}
						storing.BlockchainHeight = binary.LittleEndian.Uint64(data[0:8])
						storing.BlockchainVersion = binary.LittleEndian.Uint64(data[8:16])
						data = data[storingBlockchainInfoSize:]
					}

					record.Storing = append(record.Storing, storing)
				}

				count = countClosest
			}

			for n := 0; n < count; n++ {
				peer := PeerRecord{}
				size, err := decodePeerRecord(data, &peer)
				if err != nil {
					return nil, err
				}
				data = data[size:]

				record.Closest = append(record.Closest, peer)
			}

			// Data after the counted peer records is ignored.

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

// decodePeerRecord decodes the peer record at the start of data. It returns the size of the record, which follows from its address counts.
func decodePeerRecord(data []byte, peer *PeerRecord) (size int, err error) {
	if len(data) < peerRecordHeaderSize {
		return 0, errors.New("response: peer record overflow")
	}

	// Address counts: bits 0-1 count of IPv4 addresses, bits 2-3 count of IPv6 addresses. Bits 4-7 are reserved and ignored.
	countIPv4 := int(data[33] & 0x03)
	countIPv6 := int(data[33] >> 2 & 0x03)

	size = peerRecordHeaderSize + countIPv4*peerRecordAddressSizeIPv4 + countIPv6*peerRecordAddressSizeIPv6
	if len(data) < size {
		return 0, errors.New("response: peer record overflow")
	}

	peer.PublicKey = make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(peer.PublicKey, data[0:32])
	peer.Features = data[32]

	data = data[peerRecordHeaderSize:]

	for n := 0; n < countIPv4; n++ {
		peer.IPv4 = append(peer.IPv4, decodePeerRecordAddress(data, net.IPv4len))
		data = data[peerRecordAddressSizeIPv4:]
	}

	for n := 0; n < countIPv6; n++ {
		address := decodePeerRecordAddress(data, net.IPv6len)
		if address.IP.To4() != nil { // IPv6 address mismatch
			return 0, errors.New("response: IPv4 address in the IPv6 addresses of a peer record")
		}

		peer.IPv6 = append(peer.IPv6, address)
		data = data[peerRecordAddressSizeIPv6:]
	}

	return size, nil
}

// decodePeerRecordAddress decodes one address of a peer record. ipSize is 4 for IPv4 and 16 for IPv6. data must be at least ipSize + 6.
func decodePeerRecordAddress(data []byte, ipSize int) (address PeerRecordAddress) {
	address.IP = make(net.IP, ipSize)
	copy(address.IP, data[:ipSize])
	address.Port = binary.LittleEndian.Uint16(data[ipSize : ipSize+2])
	address.PortReportedInternal = binary.LittleEndian.Uint16(data[ipSize+2 : ipSize+4])
	address.PortReportedExternal = binary.LittleEndian.Uint16(data[ipSize+4 : ipSize+6])

	return address
}

// encodePeerRecord appends the encoded peer record to data.
func encodePeerRecord(data []byte, peer *PeerRecord) (result []byte, err error) {
	if len(peer.PublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("invalid peer ID size in peer record")
	}
	if len(peer.IPv4) > PeerRecordAddressesMax || len(peer.IPv6) > PeerRecordAddressesMax {
		return nil, errors.New("too many addresses in peer record")
	}

	data = append(data, peer.PublicKey...)
	data = append(data, peer.Features, byte(len(peer.IPv4)|len(peer.IPv6)<<2))

	for n := range peer.IPv4 {
		ip := peer.IPv4[n].IP.To4()
		if ip == nil {
			return nil, errors.New("invalid IPv4 address in peer record")
		}
		data = encodePeerRecordAddress(data, ip, &peer.IPv4[n])
	}

	for n := range peer.IPv6 {
		ip := peer.IPv6[n].IP.To16()
		if ip == nil || ip.To4() != nil {
			return nil, errors.New("invalid IPv6 address in peer record")
		}
		data = encodePeerRecordAddress(data, ip, &peer.IPv6[n])
	}

	return data, nil
}

// encodePeerRecordAddress appends one encoded address of a peer record to data. ip is the IP address in its 4 or 16 byte form.
func encodePeerRecordAddress(data []byte, ip net.IP, address *PeerRecordAddress) []byte {
	data = append(data, ip...)
	data = binary.LittleEndian.AppendUint16(data, address.Port)
	data = binary.LittleEndian.AppendUint16(data, address.PortReportedInternal)
	data = binary.LittleEndian.AppendUint16(data, address.PortReportedExternal)

	return data
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

		if len(record.Storing) > peerCountMax || len(record.Closest) > peerCountMax {
			return nil, errors.New("too many peer records")
		}

		// The first count carries the last result flag. In B_RESPONSE_BLOCKCHAIN and B_RESPONSE_FILE it is the count of storing peers, followed by the count of closest peers.
		countField := byte(len(record.Closest))
		if record.Type == ResponseBBlockchain || record.Type == ResponseBFile {
			countField = byte(len(record.Storing))
		}
		if record.IsLast {
			countField |= peerCountLastFlag
		}
		data = append(data, countField)

		if record.Type == ResponseBBlockchain || record.Type == ResponseBFile {
			data = append(data, byte(len(record.Closest)))

			for n := range record.Storing {
				if data, err = encodePeerRecord(data, &record.Storing[n].PeerRecord); err != nil {
					return nil, err
				}

				if record.Type == ResponseBBlockchain {
					data = binary.LittleEndian.AppendUint64(data, record.Storing[n].BlockchainHeight)
					data = binary.LittleEndian.AppendUint64(data, record.Storing[n].BlockchainVersion)
				}
			}
		}

		for n := range record.Closest {
			if data, err = encodePeerRecord(data, &record.Closest[n]); err != nil {
				return nil, err
			}
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
