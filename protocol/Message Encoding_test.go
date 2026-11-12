/*
File Name:  Message Encoding_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner

Round-trip tests of the Announcement and Response messages.
*/

package protocol

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"net"
	"testing"
)

func testKey(t *testing.T) ed25519.PublicKey {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return publicKey
}

func testKeys(t *testing.T, count int) (keys [][]byte) {
	for n := 0; n < count; n++ {
		keys = append(keys, []byte(testKey(t)))
	}
	return keys
}

// testPeerRecord returns a peer record with one IPv4 and one IPv6 address.
func testPeerRecord(t *testing.T, n int) PeerRecord {
	return testPeerRecordAddresses(t, n, 1, 1)
}

// testPeerRecordAddresses returns a peer record with the given count of addresses per family.
func testPeerRecordAddresses(t *testing.T, n, countIPv4, countIPv6 int) PeerRecord {
	record := PeerRecord{PublicKey: testKey(t), Features: 1<<FeatureIPv4Listen | 1<<FeatureIPv6Listen}

	for m := 0; m < countIPv4; m++ {
		record.IPv4 = append(record.IPv4, PeerRecordAddress{IP: net.IPv4(203, 0, byte(113+m), byte(n)).To4(), Port: uint16(1000 + 10*n + m), PortReportedInternal: uint16(2000 + 10*n + m), PortReportedExternal: uint16(3000 + 10*n + m)})
	}
	for m := 0; m < countIPv6; m++ {
		record.IPv6 = append(record.IPv6, PeerRecordAddress{IP: net.IP{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, byte(m), 0, byte(n)}, Port: uint16(4000 + 10*n + m), PortReportedInternal: uint16(5000 + 10*n + m), PortReportedExternal: uint16(6000 + 10*n + m)})
	}

	return record
}

// responseWithRecord returns a response message with a single response record of the given raw data.
func responseWithRecord(recordType uint8, data []byte) *MessageRaw {
	raw := append(make([]byte, announcementPayloadHeaderSize), sequenceLastFlag|1, recordType, byte(len(data)), byte(len(data)>>8))
	raw = append(raw, data...)

	msg := &MessageRaw{PacketRaw: PacketRaw{Command: CommandResponse}}
	msg.Payload = raw
	return msg
}

func decodeAll(t *testing.T, packets [][]byte) (results []*MessageAnnouncement) {
	for _, packet := range packets {
		msg := &MessageRaw{PacketRaw: PacketRaw{Command: CommandAnnouncement}}
		msg.Payload = packet
		result, err := DecodeAnnouncement(msg)
		if err != nil {
			t.Fatalf("DecodeAnnouncement: %s", err)
		}
		results = append(results, result)
	}
	return results
}

func TestAnnouncementHeader(t *testing.T) {
	packets := EncodeAnnouncement(true, nil, 1<<FeatureIPv4Listen, 7, 3, "Test Client/1.0")
	if len(packets) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(packets))
	}

	// header per whitepaper: 23 bytes + User Agent + count byte
	if len(packets[0]) != announcementPayloadHeaderSize+len("Test Client/1.0")+1 {
		t.Fatalf("unexpected size %d", len(packets[0]))
	}
	if binary.LittleEndian.Uint64(packets[0][2:10]) != 7 || binary.LittleEndian.Uint64(packets[0][10:18]) != 3 || packets[0][22] != byte(len("Test Client/1.0")) {
		t.Fatal("header fields at wrong offsets")
	}
	if packets[0][len(packets[0])-1] != 0 {
		t.Fatal("record count must be 0 without requests, SEQUENCE_LAST must be 0 in an announcement")
	}

	result := decodeAll(t, packets)[0]
	if result.UserAgent != "Test Client/1.0" || result.BlockchainHeight != 7 || result.BlockchainVersion != 3 || result.Features != 1<<FeatureIPv4Listen || !result.Requests.IsEmpty() {
		t.Fatal("decoded header mismatch")
	}
}

func TestSelfReportedPorts(t *testing.T) {
	// The packet layer writes the ports into the payload header; the offsets must match the decoder.
	for _, sendUA := range []bool{false, true} {
		packets := EncodeAnnouncement(sendUA, &Requests{FindSelf: true}, 0, 0, 0, "Test Client/1.0")
		packet := PacketRaw{Command: CommandAnnouncement, Payload: packets[0]}
		packet.SetSelfReportedPorts(20001, 443)

		result := decodeAll(t, [][]byte{packet.Payload})[0]
		if result.PortInternal != 20001 || result.PortExternal != 443 || !result.FindSelf || sendUA && result.UserAgent != "Test Client/1.0" {
			t.Fatalf("self reported ports mismatch: %+v", result)
		}
	}
}

func TestAnnouncementRequests(t *testing.T) {
	requests := &Requests{
		FindSelf:        true,
		FindPeer:        testKeys(t, 2),
		FindBlockchain:  testKeys(t, 1),
		FindFile:        testKeys(t, 3),
		FindFileEmbed:   testKeys(t, 2),
		StoreBlockchain: []BlockchainStore{{PublicKey: testKey(t), Height: 12, Version: 2}},
		StoreFile:       []FileStore{{Hash: HashData([]byte("a")), Size: 1, Type: 0}, {Hash: HashData([]byte("b")), Size: 99, Type: 1}},
	}

	packets := EncodeAnnouncement(false, requests, 0, 0, 0, "")
	if len(packets) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(packets))
	}
	if packets[0][announcementPayloadHeaderSize] != 7 {
		t.Fatalf("expected 7 records, got %d", packets[0][announcementPayloadHeaderSize])
	}

	result := decodeAll(t, packets)[0]
	if !result.FindSelf || len(result.FindPeer) != 2 || len(result.FindBlockchain) != 1 || len(result.FindFile) != 3 || len(result.FindFileEmbed) != 2 || len(result.StoreBlockchain) != 1 || len(result.StoreFile) != 2 {
		t.Fatal("record counts mismatch")
	}
	for n := range requests.FindFile {
		if !bytes.Equal(result.FindFile[n], requests.FindFile[n]) {
			t.Fatal("B_FIND_FILE key mismatch")
		}
	}
	for n := range requests.FindFileEmbed {
		if !bytes.Equal(result.FindFileEmbed[n], requests.FindFileEmbed[n]) {
			t.Fatal("B_FIND_FILE_EMBED key mismatch")
		}
	}
	if !result.StoreBlockchain[0].PublicKey.Equal(requests.StoreBlockchain[0].PublicKey) || result.StoreBlockchain[0].Height != 12 || result.StoreBlockchain[0].Version != 2 {
		t.Fatal("B_STORE_BLOCKCHAIN mismatch")
	}
	if !bytes.Equal(result.StoreFile[1].Hash, requests.StoreFile[1].Hash) || result.StoreFile[1].Size != 99 || result.StoreFile[1].Type != 1 {
		t.Fatal("B_STORE_FILE mismatch")
	}
}

func TestAnnouncementSplit(t *testing.T) {
	// 5000 keys = 160000 bytes: must be split over records (max 65535 bytes data) and messages (max ~65 KB)
	keys := testKeys(t, 5000)
	packets := EncodeAnnouncement(true, &Requests{FindPeer: keys}, 0, 0, 0, "Test Client/1.0")
	if len(packets) < 3 {
		t.Fatalf("expected at least 3 packets, got %d", len(packets))
	}

	var decoded [][]byte
	for _, result := range decodeAll(t, packets) {
		decoded = append(decoded, result.FindPeer...)
	}
	if len(decoded) != len(keys) {
		t.Fatalf("expected %d keys, got %d", len(keys), len(decoded))
	}
	for n := range keys {
		if !bytes.Equal(decoded[n], keys[n]) {
			t.Fatalf("key %d mismatch", n)
		}
	}
}

func TestAnnouncementUnknownRecord(t *testing.T) {
	packets := EncodeAnnouncement(false, &Requests{FindSelf: true}, 0, 0, 0, "")
	raw := packets[0]

	// append a record of unknown type 200 with 5 bytes data, then a known A_FIND_PEER record
	raw[announcementPayloadHeaderSize] = 3
	raw = append(raw, 200, 5, 0, 1, 2, 3, 4, 5)
	key := testKey(t)
	raw = append(raw, RequestAFindPeer, 32, 0)
	raw = append(raw, key...)

	result := decodeAll(t, [][]byte{raw})[0]
	if !result.FindSelf || len(result.FindPeer) != 1 || !bytes.Equal(result.FindPeer[0], key) {
		t.Fatal("unknown record was not skipped correctly")
	}
}

func TestAnnouncementInvalid(t *testing.T) {
	invalid := [][]byte{
		make([]byte, announcementPayloadHeaderSize), // missing count byte
		append(make([]byte, announcementPayloadHeaderSize), 1),                                   // count 1 but no record
		append(make([]byte, announcementPayloadHeaderSize), 1, RequestAFindPeer, 32, 0, 1, 2, 3), // record data overflow
		append(make([]byte, announcementPayloadHeaderSize), 1, RequestAFindPeer, 3, 0, 1, 2, 3),  // key list not a multiple of 32
	}
	invalid[0][22] = 0

	for n, raw := range invalid {
		msg := &MessageRaw{PacketRaw: PacketRaw{Command: CommandAnnouncement}}
		msg.Payload = raw
		if _, err := DecodeAnnouncement(msg); err == nil {
			t.Fatalf("invalid announcement %d accepted", n)
		}
	}
}

func TestResponseRecords(t *testing.T) {
	fileData := []byte("embedded file content")
	keyPeer := testKey(t)
	keyBlockchain := testKey(t)
	keyFile := HashData([]byte("some file"))

	records := []ResponseRecord{
		{Type: ResponseASelf, Closest: []PeerRecord{testPeerRecord(t, 1), testPeerRecordAddresses(t, 2, 3, 3)}, IsLast: true},
		{Type: ResponseASelf, Closest: []PeerRecord{testPeerRecordAddresses(t, 3, 1, 0)}, IsLast: false},
		{Type: ResponseAPeer, Key: keyPeer, Closest: []PeerRecord{testPeerRecordAddresses(t, 4, 2, 0), testPeerRecordAddresses(t, 5, 0, 2)}, IsLast: true},
		{Type: ResponseAPeer, Key: keyPeer, Closest: []PeerRecord{testPeerRecord(t, 6)}, IsLast: false},
		{Type: ResponseAPeer, Key: keyFile, IsLast: true}, // not found
		{Type: ResponseBBlockchain, Key: keyBlockchain, Storing: []StoringPeer{{PeerRecord: testPeerRecordAddresses(t, 7, 0, 0), BlockchainHeight: 10, BlockchainVersion: 1}, {PeerRecord: testPeerRecord(t, 8), BlockchainHeight: 11, BlockchainVersion: 2}}, Closest: []PeerRecord{testPeerRecord(t, 9), testPeerRecordAddresses(t, 10, 0, 1)}, IsLast: true},
		{Type: ResponseBBlockchain, Key: keyBlockchain, Closest: []PeerRecord{testPeerRecord(t, 11)}, IsLast: false},
		{Type: ResponseBBlockchain, Key: keyFile, IsLast: true}, // not found
		{Type: ResponseBFile, Key: keyFile, Storing: []StoringPeer{{PeerRecord: testPeerRecordAddresses(t, 12, 0, 0)}, {PeerRecord: testPeerRecordAddresses(t, 13, 1, 0)}}, Closest: []PeerRecord{testPeerRecord(t, 14)}, IsLast: false},
		{Type: ResponseBFile, Key: keyFile, Storing: []StoringPeer{{PeerRecord: testPeerRecord(t, 15)}}, IsLast: true},
		{Type: ResponseBFile, Key: keyBlockchain, IsLast: true}, // not found
		{Type: ResponseBFileEmbed, Key: HashData(fileData), Data: fileData},
	}

	packets, err := EncodeResponse(true, records, 1<<FeatureIPv6Listen, 5, 1, "Test Client/1.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(packets) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(packets))
	}

	msg := &MessageRaw{PacketRaw: PacketRaw{Command: CommandResponse}}
	msg.Payload = packets[0]
	result, err := DecodeResponse(msg)
	if err != nil {
		t.Fatalf("DecodeResponse: %s", err)
	}

	if !result.SequenceLast || result.UserAgent != "Test Client/1.0" || result.BlockchainHeight != 5 || result.BlockchainVersion != 1 {
		t.Fatal("header mismatch")
	}
	if len(result.Records) != len(records) {
		t.Fatalf("expected %d records, got %d", len(records), len(result.Records))
	}

	for n, record := range result.Records {
		want := records[n]
		if record.Type != want.Type || !bytes.Equal(record.Key, want.Key) || record.IsLast != want.IsLast || len(record.Closest) != len(want.Closest) || len(record.Storing) != len(want.Storing) || !bytes.Equal(record.Data, want.Data) {
			t.Fatalf("record %d mismatch: %+v", n, record)
		}
		for m := range want.Closest {
			comparePeerRecord(t, &record.Closest[m], &want.Closest[m])
		}
		for m := range want.Storing {
			comparePeerRecord(t, &record.Storing[m].PeerRecord, &want.Storing[m].PeerRecord)
			if record.Storing[m].BlockchainHeight != want.Storing[m].BlockchainHeight || record.Storing[m].BlockchainVersion != want.Storing[m].BlockchainVersion {
				t.Fatalf("record %d storing peer %d height/version mismatch", n, m)
			}
		}
	}
}

func comparePeerRecord(t *testing.T, got, want *PeerRecord) {
	equal := func(got, want []PeerRecordAddress) bool {
		if len(got) != len(want) {
			return false
		}
		for n := range want {
			if !got[n].IP.Equal(want[n].IP) || got[n].Port != want[n].Port || got[n].PortReportedInternal != want[n].PortReportedInternal || got[n].PortReportedExternal != want[n].PortReportedExternal {
				return false
			}
		}
		return true
	}

	if !got.PublicKey.Equal(want.PublicKey) || got.Features != want.Features || !equal(got.IPv4, want.IPv4) || !equal(got.IPv6, want.IPv6) {
		t.Fatalf("peer record mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// TestPeerRecordSize checks the size and the layout of peer records by their address counts.
func TestPeerRecordSize(t *testing.T) {
	tests := []struct {
		countIPv4, countIPv6, size int
	}{
		{1, 0, 44},
		{0, 1, 56},
		{1, 1, 66},
		{3, 3, 130},
		{0, 0, 34},
	}

	for _, test := range tests {
		want := testPeerRecordAddresses(t, 1, test.countIPv4, test.countIPv6)

		raw, err := encodePeerRecord(nil, &want)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) != test.size || want.Size() != test.size {
			t.Fatalf("%d IPv4 + %d IPv6 addresses: encoded %d bytes, Size() %d, expected %d", test.countIPv4, test.countIPv6, len(raw), want.Size(), test.size)
		}

		// layout: Peer ID, features, address counts, IPv4 addresses, IPv6 addresses
		if !bytes.Equal(raw[0:32], want.PublicKey) || raw[32] != want.Features || raw[33] != byte(test.countIPv4|test.countIPv6<<2) {
			t.Fatalf("%d IPv4 + %d IPv6 addresses: fields at wrong offsets", test.countIPv4, test.countIPv6)
		}
		if test.countIPv4 > 0 && (!bytes.Equal(raw[34:38], want.IPv4[0].IP.To4()) || binary.LittleEndian.Uint16(raw[38:40]) != want.IPv4[0].Port || binary.LittleEndian.Uint16(raw[40:42]) != want.IPv4[0].PortReportedInternal || binary.LittleEndian.Uint16(raw[42:44]) != want.IPv4[0].PortReportedExternal) {
			t.Fatalf("%d IPv4 + %d IPv6 addresses: IPv4 address at wrong offsets", test.countIPv4, test.countIPv6)
		}
		if o := 34 + 10*test.countIPv4; test.countIPv6 > 0 && (!bytes.Equal(raw[o:o+16], want.IPv6[0].IP) || binary.LittleEndian.Uint16(raw[o+16:o+18]) != want.IPv6[0].Port || binary.LittleEndian.Uint16(raw[o+18:o+20]) != want.IPv6[0].PortReportedInternal || binary.LittleEndian.Uint16(raw[o+20:o+22]) != want.IPv6[0].PortReportedExternal) {
			t.Fatalf("%d IPv4 + %d IPv6 addresses: IPv6 address at wrong offsets", test.countIPv4, test.countIPv6)
		}

		var got PeerRecord
		size, err := decodePeerRecord(append(raw, 0xFF, 0xFF), &got)
		if err != nil || size != test.size {
			t.Fatalf("%d IPv4 + %d IPv6 addresses: decoded size %d (%v), expected %d", test.countIPv4, test.countIPv6, size, err, test.size)
		}
		comparePeerRecord(t, &got, &want)

		// a peer record cut short is rejected
		if _, err := decodePeerRecord(raw[:len(raw)-1], &got); err == nil {
			t.Fatalf("%d IPv4 + %d IPv6 addresses: truncated peer record accepted", test.countIPv4, test.countIPv6)
		}

		// storing peer record in B_RESPONSE_BLOCKCHAIN: peer record followed by height (8) and version (8)
		key := testKey(t)
		data, err := encodeResponseRecord(&ResponseRecord{Type: ResponseBBlockchain, Key: key, Storing: []StoringPeer{{PeerRecord: want, BlockchainHeight: 7, BlockchainVersion: 3}}, IsLast: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(data) != 32+2+test.size+16 || binary.LittleEndian.Uint64(data[34+test.size:]) != 7 || binary.LittleEndian.Uint64(data[34+test.size+8:]) != 3 {
			t.Fatalf("%d IPv4 + %d IPv6 addresses: storing peer record in B_RESPONSE_BLOCKCHAIN of %d bytes", test.countIPv4, test.countIPv6, len(data)-34)
		}
	}
}

// TestResponseCounts checks the one-byte counts of peer records at their offsets in the response data.
func TestResponseCounts(t *testing.T) {
	key := testKey(t)
	closest := []PeerRecord{testPeerRecord(t, 1), testPeerRecord(t, 2), testPeerRecord(t, 3)}
	storing := []StoringPeer{{PeerRecord: testPeerRecord(t, 4)}, {PeerRecord: testPeerRecord(t, 5)}}

	tests := []struct {
		record ResponseRecord
		offset int    // offset of the first count
		counts []byte // expected count bytes
	}{
		{ResponseRecord{Type: ResponseASelf, Closest: closest, IsLast: true}, 0, []byte{0x80 | 3}},
		{ResponseRecord{Type: ResponseASelf, Closest: closest}, 0, []byte{3}},
		{ResponseRecord{Type: ResponseASelf, IsLast: true}, 0, []byte{0x80}},
		{ResponseRecord{Type: ResponseAPeer, Key: key, Closest: closest, IsLast: true}, 32, []byte{0x80 | 3}},
		{ResponseRecord{Type: ResponseAPeer, Key: key, Closest: closest}, 32, []byte{3}},
		{ResponseRecord{Type: ResponseAPeer, Key: key, IsLast: true}, 32, []byte{0x80}}, // not found
		{ResponseRecord{Type: ResponseBBlockchain, Key: key, Storing: storing, Closest: closest, IsLast: true}, 32, []byte{0x80 | 2, 3}},
		{ResponseRecord{Type: ResponseBBlockchain, Key: key, Storing: storing, Closest: closest}, 32, []byte{2, 3}},
		{ResponseRecord{Type: ResponseBBlockchain, Key: key, IsLast: true}, 32, []byte{0x80, 0}}, // not found
		{ResponseRecord{Type: ResponseBFile, Key: key, Storing: storing, Closest: closest, IsLast: true}, 32, []byte{0x80 | 2, 3}},
		{ResponseRecord{Type: ResponseBFile, Key: key, Closest: closest}, 32, []byte{0, 3}},
		{ResponseRecord{Type: ResponseBFile, Key: key, IsLast: true}, 32, []byte{0x80, 0}}, // not found
	}

	for n, test := range tests {
		data, err := encodeResponseRecord(&test.record)
		if err != nil {
			t.Fatal(err)
		}

		size := test.offset + len(test.counts) + len(test.record.Closest)*66 + len(test.record.Storing)*66
		if test.record.Type == ResponseBBlockchain {
			size += len(test.record.Storing) * 16
		}
		if len(data) != size || !bytes.Equal(data[test.offset:test.offset+len(test.counts)], test.counts) {
			t.Fatalf("test %d: %d bytes with counts %v, expected %d bytes with counts %v", n, len(data), data[test.offset:test.offset+len(test.counts)], size, test.counts)
		}

		result, err := DecodeResponse(responseWithRecord(test.record.Type, data))
		if err != nil || len(result.Records) != 1 {
			t.Fatalf("test %d: DecodeResponse: %v", n, err)
		}
		if record := result.Records[0]; record.IsLast != test.record.IsLast || len(record.Closest) != len(test.record.Closest) || len(record.Storing) != len(test.record.Storing) {
			t.Fatalf("test %d: decoded record mismatch: %+v", n, record)
		}
	}
}

// TestResponseTrailingData checks that data after the counted peer records is ignored.
func TestResponseTrailingData(t *testing.T) {
	key := testKey(t)
	records := []ResponseRecord{
		{Type: ResponseASelf, Closest: []PeerRecord{testPeerRecord(t, 1)}, IsLast: true},
		{Type: ResponseAPeer, Key: key, Closest: []PeerRecord{testPeerRecord(t, 2), testPeerRecordAddresses(t, 3, 3, 3)}, IsLast: true},
		{Type: ResponseAPeer, Key: key, IsLast: true}, // not found
		{Type: ResponseBBlockchain, Key: key, Storing: []StoringPeer{{PeerRecord: testPeerRecordAddresses(t, 4, 0, 0), BlockchainHeight: 10, BlockchainVersion: 1}}, Closest: []PeerRecord{testPeerRecord(t, 5)}, IsLast: true},
		{Type: ResponseBFile, Key: key, Storing: []StoringPeer{{PeerRecord: testPeerRecordAddresses(t, 6, 0, 0)}}, Closest: []PeerRecord{testPeerRecord(t, 7)}, IsLast: true},
	}

	for n := range records {
		want := records[n]
		data, err := encodeResponseRecord(&want)
		if err != nil {
			t.Fatal(err)
		}

		// The trailing data has the size of a peer record without addresses and of one with addresses.
		for _, trailing := range [][]byte{{0xFF}, make([]byte, 34), bytes.Repeat([]byte{0xFF}, 66)} {
			result, err := DecodeResponse(responseWithRecord(want.Type, append(append([]byte{}, data...), trailing...)))
			if err != nil || len(result.Records) != 1 {
				t.Fatalf("record %d with %d bytes trailing data: %v", n, len(trailing), err)
			}

			record := result.Records[0]
			if record.IsLast != want.IsLast || len(record.Closest) != len(want.Closest) || len(record.Storing) != len(want.Storing) {
				t.Fatalf("record %d with %d bytes trailing data: decoded record mismatch: %+v", n, len(trailing), record)
			}
			for m := range want.Closest {
				comparePeerRecord(t, &record.Closest[m], &want.Closest[m])
			}
			for m := range want.Storing {
				comparePeerRecord(t, &record.Storing[m].PeerRecord, &want.Storing[m].PeerRecord)
			}
		}
	}
}

// TestResponseEncodeLimits checks that the encoder refuses what the counts cannot express.
func TestResponseEncodeLimits(t *testing.T) {
	key := testKey(t)

	// 3 addresses per family are the maximum
	for _, counts := range [][2]int{{4, 0}, {0, 4}, {4, 4}} {
		record := testPeerRecordAddresses(t, 1, counts[0], counts[1])
		if _, err := encodePeerRecord(nil, &record); err == nil {
			t.Fatalf("peer record with %d IPv4 and %d IPv6 addresses accepted", counts[0], counts[1])
		}
		if _, err := EncodeResponse(false, []ResponseRecord{{Type: ResponseAPeer, Key: key, Closest: []PeerRecord{record}, IsLast: true}}, 0, 0, 0, ""); err == nil {
			t.Fatalf("response with a peer record with %d IPv4 and %d IPv6 addresses accepted", counts[0], counts[1])
		}
	}

	// 127 peer records per list are the maximum
	peers := func(count int) (closest []PeerRecord, storing []StoringPeer) {
		for n := 0; n < count; n++ {
			closest = append(closest, testPeerRecordAddresses(t, n, 1, 0))
			storing = append(storing, StoringPeer{PeerRecord: testPeerRecordAddresses(t, n, 1, 0)})
		}
		return
	}
	closest127, storing127 := peers(127)
	closest128, storing128 := peers(128)

	accepted := []ResponseRecord{
		{Type: ResponseASelf, Closest: closest127, IsLast: true},
		{Type: ResponseAPeer, Key: key, Closest: closest127, IsLast: true},
		{Type: ResponseBBlockchain, Key: key, Storing: storing127, Closest: closest127, IsLast: true},
		{Type: ResponseBFile, Key: key, Storing: storing127, Closest: closest127, IsLast: true},
	}
	for n := range accepted {
		data, err := encodeResponseRecord(&accepted[n])
		if err != nil {
			t.Fatalf("record %d with 127 peer records per list refused: %s", n, err)
		}
		result, err := DecodeResponse(responseWithRecord(accepted[n].Type, data))
		if err != nil || len(result.Records) != 1 || len(result.Records[0].Closest) != 127 || len(result.Records[0].Storing) != len(accepted[n].Storing) || !result.Records[0].IsLast {
			t.Fatalf("record %d with 127 peer records per list: decode mismatch (%v)", n, err)
		}
	}

	refused := []ResponseRecord{
		{Type: ResponseASelf, Closest: closest128, IsLast: true},
		{Type: ResponseAPeer, Key: key, Closest: closest128, IsLast: true},
		{Type: ResponseBBlockchain, Key: key, Storing: storing128, IsLast: true},
		{Type: ResponseBBlockchain, Key: key, Closest: closest128, IsLast: true},
		{Type: ResponseBFile, Key: key, Storing: storing128, IsLast: true},
		{Type: ResponseBFile, Key: key, Closest: closest128, IsLast: true},
	}
	for n := range refused {
		if _, err := encodeResponseRecord(&refused[n]); err == nil {
			t.Fatalf("record %d with 128 peer records in one list accepted", n)
		}
	}

	// an address of the wrong family
	record := testPeerRecordAddresses(t, 1, 1, 1)
	record.IPv4[0].IP, record.IPv6[0].IP = record.IPv6[0].IP, record.IPv4[0].IP
	if _, err := encodePeerRecord(nil, &record); err == nil {
		t.Fatal("peer record with addresses of the wrong family accepted")
	}
}

func TestResponseEmpty(t *testing.T) {
	packets, err := EncodeResponse(false, nil, 0, 0, 0, "")
	if err != nil || len(packets) != 1 {
		t.Fatalf("expected 1 packet, got %d (%v)", len(packets), err)
	}
	if packets[0][announcementPayloadHeaderSize] != sequenceLastFlag {
		t.Fatal("empty response must carry count 0 with SEQUENCE_LAST")
	}

	msg := &MessageRaw{PacketRaw: PacketRaw{Command: CommandResponse}}
	msg.Payload = packets[0]
	result, err := DecodeResponse(msg)
	if err != nil || !result.SequenceLast || len(result.Records) != 0 {
		t.Fatal("empty response decode mismatch")
	}
}

func TestResponseSplit(t *testing.T) {
	// 40 records of 100 closest peers each = 40 * (32+1+6600) bytes: must be split over messages, only the last with SEQUENCE_LAST
	var records []ResponseRecord
	for n := 0; n < 40; n++ {
		record := ResponseRecord{Type: ResponseAPeer, Key: testKey(t), IsLast: true}
		for m := 0; m < 100; m++ {
			record.Closest = append(record.Closest, testPeerRecord(t, m))
		}
		records = append(records, record)
	}

	packets, err := EncodeResponse(true, records, 0, 0, 0, "Test Client/1.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(packets) < 4 {
		t.Fatalf("expected at least 4 packets, got %d", len(packets))
	}

	total := 0
	for n, packet := range packets {
		if len(packet) > messagePayloadMax {
			t.Fatalf("packet %d exceeds the maximum payload", n)
		}
		msg := &MessageRaw{PacketRaw: PacketRaw{Command: CommandResponse}}
		msg.Payload = packet
		result, err := DecodeResponse(msg)
		if err != nil {
			t.Fatalf("DecodeResponse packet %d: %s", n, err)
		}
		if result.SequenceLast != (n == len(packets)-1) {
			t.Fatalf("packet %d: SEQUENCE_LAST wrong", n)
		}
		for _, record := range result.Records {
			if !bytes.Equal(record.Key, records[total].Key) || len(record.Closest) != 100 {
				t.Fatalf("record %d mismatch", total)
			}
			total++
		}
	}
	if total != len(records) {
		t.Fatalf("expected %d records, got %d", len(records), total)
	}
}

func TestResponseInvalid(t *testing.T) {
	fileData := []byte("embedded file content")

	// embedded file with wrong hash
	packets, err := EncodeResponse(false, []ResponseRecord{{Type: ResponseBFileEmbed, Key: HashData([]byte("other")), Data: fileData}}, 0, 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	msg := &MessageRaw{PacketRaw: PacketRaw{Command: CommandResponse}}
	msg.Payload = packets[0]
	if _, err := DecodeResponse(msg); err == nil {
		t.Fatal("embedded file with wrong hash accepted")
	}

	// embedded file too big
	if _, err := EncodeResponse(false, []ResponseRecord{{Type: ResponseBFileEmbed, Key: HashData(nil), Data: make([]byte, EmbeddedFileSizeMax+1)}}, 0, 0, 0, ""); err == nil {
		t.Fatal("oversized embedded file accepted")
	}

	// a count that runs past the data
	key := testKey(t)
	peer1, peer2 := testPeerRecord(t, 1), testPeerRecord(t, 2)
	raw1, _ := encodePeerRecord(nil, &peer1)
	raw2, _ := encodePeerRecord(nil, &peer2)

	join := func(parts ...[]byte) []byte {
		return bytes.Join(parts, nil)
	}

	overrun := []struct {
		recordType uint8
		data       []byte
	}{
		{ResponseASelf, []byte{0x80 | 1}},
		{ResponseASelf, join([]byte{0x80 | 2}, raw1)},
		{ResponseASelf, join([]byte{0x80 | 2}, raw1, raw2[:len(raw2)-1])},
		{ResponseAPeer, join(key, []byte{0x80 | 2})},
		{ResponseAPeer, join(key, []byte{0x80 | 2}, raw1, raw2[:33])},
		{ResponseBBlockchain, join(key, []byte{0x80 | 1, 0}, raw1)},                   // height and version missing
		{ResponseBBlockchain, join(key, []byte{0x80 | 1, 0}, raw1, make([]byte, 15))}, // version cut short
		{ResponseBBlockchain, join(key, []byte{0x80 | 1, 1}, raw1, make([]byte, 16))}, // closest peer missing
		{ResponseBBlockchain, join(key, []byte{0x80 | 1})},                            // second count missing
		{ResponseBFile, join(key, []byte{0x80 | 2, 0}, raw1)},                         // second storing peer missing
		{ResponseBFile, join(key, []byte{0x80 | 1, 1}, raw1, raw2[:len(raw2)-1])},     // closest peer cut short
		{ResponseBFile, join(key, []byte{0x80, 127})},                                 // closest peers missing
		{ResponseAPeer, key[:31]},                                                     // key cut short
		{ResponseAPeer, key},                                                          // count missing
	}
	for n, test := range overrun {
		if _, err := DecodeResponse(responseWithRecord(test.recordType, test.data)); err == nil {
			t.Fatalf("overrun %d: record with a count that runs past the data accepted", n)
		}
	}

	// bits 4-7 of the address counts are reserved: they are ignored and the record decodes as without them
	for bit := 4; bit < 8; bit++ {
		reserved := append([]byte{}, raw1...)
		reserved[33] |= 1 << bit

		var record PeerRecord
		if size, err := decodePeerRecord(reserved, &record); err != nil || size != len(raw1) {
			t.Fatalf("peer record with bit %d of the address counts set: size %d (%v)", bit, size, err)
		}
		comparePeerRecord(t, &record, &peer1)

		result, err := DecodeResponse(responseWithRecord(ResponseAPeer, join(key, []byte{0x80 | 2}, reserved, raw2)))
		if err != nil || len(result.Records) != 1 || len(result.Records[0].Closest) != 2 {
			t.Fatalf("response with a peer record with bit %d of the address counts set: %v", bit, err)
		}
		comparePeerRecord(t, &result.Records[0].Closest[0], &peer1)
		comparePeerRecord(t, &result.Records[0].Closest[1], &peer2)

		result, err = DecodeResponse(responseWithRecord(ResponseBFile, join(key, []byte{0x80 | 1, 1}, reserved, raw2)))
		if err != nil || len(result.Records) != 1 || len(result.Records[0].Storing) != 1 || len(result.Records[0].Closest) != 1 {
			t.Fatalf("response with a storing peer record with bit %d of the address counts set: %v", bit, err)
		}
		comparePeerRecord(t, &result.Records[0].Storing[0].PeerRecord, &peer1)
		comparePeerRecord(t, &result.Records[0].Closest[0], &peer2)
	}

	// an IPv4 address (IPv4-mapped) in the IPv6 addresses
	mapped := append([]byte{}, raw1...)
	copy(mapped[34+10:], net.ParseIP("203.0.113.7").To16())
	if _, err := DecodeResponse(responseWithRecord(ResponseAPeer, join(key, []byte{0x80 | 1}, mapped))); err == nil {
		t.Fatal("peer record with an IPv4 address in the IPv6 addresses accepted")
	}

	// bit 7 of the second count is not part of the count
	result, err := DecodeResponse(responseWithRecord(ResponseBFile, join(key, []byte{1, 0x80 | 1}, raw1, raw2)))
	if err != nil || len(result.Records) != 1 || len(result.Records[0].Storing) != 1 || len(result.Records[0].Closest) != 1 || result.Records[0].IsLast {
		t.Fatalf("second count with bit 7 set: %v %+v", err, result)
	}

	// unknown record type is skipped, following record decoded
	packets, _ = EncodeResponse(false, []ResponseRecord{{Type: ResponseASelf, IsLast: true}}, 0, 0, 0, "")
	raw := packets[0]
	raw[announcementPayloadHeaderSize] = sequenceLastFlag | 3
	raw = append(raw, 200, 1, 0, 0xFF)
	raw = append(raw, ResponseASelf, 1, 0, 0x80)
	msg.Payload = raw
	result, err = DecodeResponse(msg)
	if err != nil || len(result.Records) != 2 || result.Records[1].Type != ResponseASelf || !result.Records[1].IsLast {
		t.Fatalf("unknown record not skipped: %v %+v", err, result)
	}
}
