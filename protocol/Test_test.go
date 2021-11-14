// Functions to manually debug encoding/decoding. No actual automated unit tests.
package protocol

import (
	"fmt"
	"testing"

	"github.com/PeernetOfficial/core/btcec"
)

func TestMessageEncodingAnnouncement(t *testing.T) {
	privateKey, err := btcec.NewPrivateKey(btcec.S256())
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		return
	}
	publicKey := (*btcec.PublicKey)(&privateKey.PublicKey)

	// encode and decode announcement
	packetR := PacketRaw{Protocol: 0, Command: CommandAnnouncement, Sequence: 123}

	var findPeer []KeyHash
	var findValue []KeyHash
	var files []InfoStore

	hash1 := HashData([]byte("test"))
	hash2 := HashData([]byte("test3"))
	findPeer = append(findPeer, KeyHash{Hash: hash1})
	findValue = append(findValue, KeyHash{Hash: hash2})

	packets := EncodeAnnouncement(true, true, findPeer, findValue, files, 1<<FeatureIPv4Listen|1<<FeatureIPv6Listen, 0, 0, "Debug Test/1.0")

	msg := &MessageRaw{PacketRaw: packetR, SenderPublicKey: publicKey}
	msg.Payload = packets[0]

	result, err := DecodeAnnouncement(msg)
	if err != nil {
		fmt.Printf("Error DecodeAnnouncement: %s\n", err.Error())
		return
	}

	fmt.Printf("Decode:\nUser Agent: %s\nFind Peer: %v\nFind Data: %v\n", result.UserAgent, result.FindPeerKeys, result.FindDataKeys)
}

func TestMessageEncodingResponse(t *testing.T) {
	privateKey, err := btcec.NewPrivateKey(btcec.S256())
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		return
	}
	publicKey := (*btcec.PublicKey)(&privateKey.PublicKey)

	// encode and decode response
	packetR := PacketRaw{Protocol: 0, Command: CommandResponse}

	var hash2Peers []Hash2Peer
	var filesEmbed []EmbeddedFileData
	var hashesNotFound [][]byte

	file1Data := []byte("test")
	file2Data := []byte("test3")
	file1 := EmbeddedFileData{ID: KeyHash{HashData(file1Data)}, Data: file1Data}
	file2 := EmbeddedFileData{ID: KeyHash{HashData(file2Data)}, Data: file2Data}
	filesEmbed = append(filesEmbed, file1)
	filesEmbed = append(filesEmbed, file2)

	hashesNotFound = append(hashesNotFound, HashData([]byte("NA")))

	packetsRaw, err := EncodeResponse(true, hash2Peers, filesEmbed, hashesNotFound, 1<<FeatureIPv4Listen|1<<FeatureIPv6Listen, 0, 0, "Debug Test/1.0")
	if err != nil {
		fmt.Printf("Error msgEncodeAnnouncement: %s\n", err.Error())
		return
	}

	msg := &MessageRaw{PacketRaw: packetR, SenderPublicKey: publicKey}
	msg.Payload = packetsRaw[0]

	result, err := DecodeResponse(msg)
	if err != nil {
		fmt.Printf("Error DecodeAnnouncement: %s\n", err.Error())
		return
	}

	fmt.Printf("Decode:\nUser Agent: %s\nHash2Peers: %v\nHashesNotFound: %v\nFiles embedded: %v\n", result.UserAgent, result.Hash2Peers, result.HashesNotFound, result.FilesEmbed)
}
