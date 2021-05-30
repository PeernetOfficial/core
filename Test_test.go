// Functions to manually debug encoding/decoding. No actual automated unit tests.
package core

import (
	"encoding/hex"
	"fmt"
	"testing"
)

func TestMessageEncodingAnnouncement(t *testing.T) {
	_, publicKey, err := Secp256k1NewPrivateKey()
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		return
	}

	// encode and decode announcement
	packetR := PacketRaw{Protocol: 0, Command: CommandAnnouncement, Sequence: 123}

	var findPeer []KeyHash
	var findValue []KeyHash
	var files []InfoStore

	hash1 := hashData([]byte("test"))
	hash2 := hashData([]byte("test3"))
	findPeer = append(findPeer, KeyHash{Hash: hash1})
	findValue = append(findValue, KeyHash{Hash: hash2})

	packets := msgEncodeAnnouncement(true, true, findPeer, findValue, files)

	msg := &MessageRaw{PacketRaw: packetR, SenderPublicKey: publicKey}
	msg.Payload = packets[0].raw

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
	packetR := PacketRaw{Protocol: 0, Command: CommandResponse}

	var hash2Peers []Hash2Peer
	var filesEmbed []EmbeddedFileData
	var hashesNotFound [][]byte

	file1Data := []byte("test")
	file2Data := []byte("test3")
	file1 := EmbeddedFileData{ID: KeyHash{hashData(file1Data)}, Data: file1Data}
	file2 := EmbeddedFileData{ID: KeyHash{hashData(file2Data)}, Data: file2Data}
	filesEmbed = append(filesEmbed, file1)
	filesEmbed = append(filesEmbed, file2)

	hashesNotFound = append(hashesNotFound, hashData([]byte("NA")))

	packetsRaw, err := msgEncodeResponse(true, hash2Peers, filesEmbed, hashesNotFound)
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

func TestBlockEncoding(t *testing.T) {
	privateKey, _, err := Secp256k1NewPrivateKey()
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		return
	}

	file1 := BlockRecordFile{Hash: hashData([]byte("Test data")), Type: TypeText, Format: FormatText, Size: 9, Name: "Filename 1.txt", Directory: "documents\\sub folder"}

	block := &Block{BlockchainVersion: 42, Number: 0, User: BlockRecordUser{Valid: true, Name: "Test User 1"}, Files: []BlockRecordFile{file1}}

	raw, err := encodeBlock(block, privateKey)
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		return
	}

	block, err = decodeBlock(raw)
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		return
	}

	// output the block details
	fmt.Printf("Block details:\n----------------\nNumber: %d\nVersion: %d\nLast Hash: %s\nPublic Key: %s\n", block.Number, block.BlockchainVersion, hex.EncodeToString(block.LastBlockHash), hex.EncodeToString(block.OwnerPublicKey.SerializeCompressed()))

	for _, file := range block.Files {
		fmt.Printf("* File          %s\n", file.Name)
		fmt.Printf("  Directory     %s\n", file.Directory)
		fmt.Printf("  Size          %d\n", file.Size)
		fmt.Printf("  Type          %d\n", file.Type)
		fmt.Printf("  Format        %d\n", file.Format)
		fmt.Printf("  Hash          %s\n", hex.EncodeToString(file.Hash))
		fmt.Printf("  Directory ID  %d\n\n", file.directoryID)
	}
}
