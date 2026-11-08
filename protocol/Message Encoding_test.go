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

func testPeerRecord(t *testing.T, n int) PeerRecord {
	return PeerRecord{
		PublicKey:                testKey(t),
		IPv4:                     net.IPv4(10, 0, 0, byte(n)).To4(),
		IPv4Port:                 uint16(1000 + n),
		IPv4PortReportedInternal: uint16(2000 + n),
		IPv4PortReportedExternal: uint16(3000 + n),
		IPv6:                     net.ParseIP("2001:db8::1"),
		IPv6Port:                 uint16(4000 + n),
		IPv6PortReportedInternal: uint16(5000 + n),
		IPv6PortReportedExternal: uint16(6000 + n),
		LastContact:              uint32(n),
		Features:                 1<<FeatureIPv4Listen | 1<<FeatureIPv6Listen,
	}
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
		StoreBlockchain: []BlockchainStore{{PublicKey: testKey(t), Height: 12, Version: 2}},
		StoreFile:       []FileStore{{Hash: HashData([]byte("a")), Size: 1, Type: 0}, {Hash: HashData([]byte("b")), Size: 99, Type: 1}},
	}

	packets := EncodeAnnouncement(false, requests, 0, 0, 0, "")
	if len(packets) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(packets))
	}
	if packets[0][announcementPayloadHeaderSize] != 6 {
		t.Fatalf("expected 6 records, got %d", packets[0][announcementPayloadHeaderSize])
	}

	result := decodeAll(t, packets)[0]
	if !result.FindSelf || len(result.FindPeer) != 2 || len(result.FindBlockchain) != 1 || len(result.FindFile) != 3 || len(result.StoreBlockchain) != 1 || len(result.StoreFile) != 2 {
		t.Fatal("record counts mismatch")
	}
	for n := range requests.FindFile {
		if !bytes.Equal(result.FindFile[n], requests.FindFile[n]) {
			t.Fatal("B_FIND_FILE key mismatch")
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
		{Type: ResponseASelf, Closest: []PeerRecord{testPeerRecord(t, 1), testPeerRecord(t, 2)}, IsLast: true},
		{Type: ResponseAPeer, Key: keyPeer, Closest: []PeerRecord{testPeerRecord(t, 3)}, IsLast: true},
		{Type: ResponseAPeer, Key: keyFile, IsLast: true}, // not found
		{Type: ResponseBBlockchain, Key: keyBlockchain, Storing: []StoringPeer{{PeerRecord: testPeerRecord(t, 4), BlockchainHeight: 10, BlockchainVersion: 1}}, Closest: []PeerRecord{testPeerRecord(t, 5), testPeerRecord(t, 6)}, IsLast: true},
		{Type: ResponseBFile, Key: keyFile, Storing: []StoringPeer{{PeerRecord: testPeerRecord(t, 7)}, {PeerRecord: testPeerRecord(t, 8)}}, Closest: []PeerRecord{testPeerRecord(t, 9)}, IsLast: false},
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
	if !got.PublicKey.Equal(want.PublicKey) || !got.IPv4.Equal(want.IPv4) || got.IPv4Port != want.IPv4Port || got.IPv4PortReportedInternal != want.IPv4PortReportedInternal || got.IPv4PortReportedExternal != want.IPv4PortReportedExternal ||
		!got.IPv6.Equal(want.IPv6) || got.IPv6Port != want.IPv6Port || got.IPv6PortReportedInternal != want.IPv6PortReportedInternal || got.IPv6PortReportedExternal != want.IPv6PortReportedExternal ||
		got.LastContact != want.LastContact || got.Features != want.Features {
		t.Fatalf("peer record mismatch:\n got %+v\nwant %+v", got, want)
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
	// 40 records of 100 closest peers each = 40 * (32+2+6900) bytes: must be split over messages, only the last with SEQUENCE_LAST
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

	// A_RESPONSE_PEER record with a count that exceeds the data
	raw := append(make([]byte, announcementPayloadHeaderSize), sequenceLastFlag|1, ResponseAPeer, 34, 0)
	raw = append(raw, make([]byte, 32)...)
	raw = append(raw, 2, 0)
	msg.Payload = raw
	if _, err := DecodeResponse(msg); err == nil {
		t.Fatal("truncated peer records accepted")
	}

	// unknown record type is skipped, following record decoded
	packets, _ = EncodeResponse(false, []ResponseRecord{{Type: ResponseASelf, IsLast: true}}, 0, 0, 0, "")
	raw = packets[0]
	raw[announcementPayloadHeaderSize] = sequenceLastFlag | 3
	raw = append(raw, 200, 1, 0, 0xFF)
	raw = append(raw, ResponseASelf, 2, 0, 0, 0x80)
	msg.Payload = raw
	result, err := DecodeResponse(msg)
	if err != nil || len(result.Records) != 2 || result.Records[1].Type != ResponseASelf || !result.Records[1].IsLast {
		t.Fatalf("unknown record not skipped: %v %+v", err, result)
	}
}
