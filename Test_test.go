// Functions to manually debug encoding/decoding. No actual automated unit tests.
package core

import (
	"fmt"
	"testing"

	"github.com/PeernetOfficial/core/protocol"
)

func TestMessageEncodingAnnouncement(t *testing.T) {
	_, publicKey, err := Secp256k1NewPrivateKey()
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		return
	}

	// encode and decode announcement
	packetR := protocol.PacketRaw{Protocol: 0, Command: protocol.CommandAnnouncement, Sequence: 123}

	var findPeer []KeyHash
	var findValue []KeyHash
	var files []InfoStore

	hash1 := protocol.HashData([]byte("test"))
	hash2 := protocol.HashData([]byte("test3"))
	findPeer = append(findPeer, KeyHash{Hash: hash1})
	findValue = append(findValue, KeyHash{Hash: hash2})

	packets := EncodeAnnouncement(true, true, findPeer, findValue, files, 1<<FeatureIPv4Listen|1<<FeatureIPv6Listen, 0, 0)

	msg := &MessageRaw{PacketRaw: packetR, SenderPublicKey: publicKey}
	msg.Payload = packets[0]

	result, err := msgDecodeAnnouncement(msg)
	if err != nil {
		fmt.Printf("Error msgDecodeAnnouncement: %s\n", err.Error())
		return
	}

	fmt.Printf("Decode:\nUser Agent: %s\nFind Peer: %v\nFind Data: %v\n", result.UserAgent, result.FindPeerKeys, result.FindDataKeys)
}

func TestMessageEncodingResponse(t *testing.T) {
	_, publicKey, err := Secp256k1NewPrivateKey()
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		return
	}

	// encode and decode response
	packetR := protocol.PacketRaw{Protocol: 0, Command: protocol.CommandResponse}

	var hash2Peers []Hash2Peer
	var filesEmbed []EmbeddedFileData
	var hashesNotFound [][]byte

	file1Data := []byte("test")
	file2Data := []byte("test3")
	file1 := EmbeddedFileData{ID: KeyHash{protocol.HashData(file1Data)}, Data: file1Data}
	file2 := EmbeddedFileData{ID: KeyHash{protocol.HashData(file2Data)}, Data: file2Data}
	filesEmbed = append(filesEmbed, file1)
	filesEmbed = append(filesEmbed, file2)

	hashesNotFound = append(hashesNotFound, protocol.HashData([]byte("NA")))

	packetsRaw, err := msgEncodeResponse(true, hash2Peers, filesEmbed, hashesNotFound, 1<<FeatureIPv4Listen|1<<FeatureIPv6Listen, 0, 0)
	if err != nil {
		fmt.Printf("Error msgEncodeAnnouncement: %s\n", err.Error())
		return
	}

	msg := &MessageRaw{PacketRaw: packetR, SenderPublicKey: publicKey}
	msg.Payload = packetsRaw[0]

	result, err := msgDecodeResponse(msg)
	if err != nil {
		fmt.Printf("Error msgDecodeAnnouncement: %s\n", err.Error())
		return
	}

	fmt.Printf("Decode:\nUser Agent: %s\nHash2Peers: %v\nHashesNotFound: %v\nFiles embedded: %v\n", result.UserAgent, result.Hash2Peers, result.HashesNotFound, result.FilesEmbed)
}
