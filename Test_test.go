// Functions to manually debug encoding/decoding. No actual automated unit tests.
package core

import (
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/btcec"
	"github.com/google/uuid"
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

	encoded1, _ := encodeBlockRecordUser(BlockRecordUser{Name: "Test User 1"})

	file1 := BlockRecordFile{Hash: hashData([]byte("Test data")), Type: TypeText, Format: FormatText, Size: 9, ID: uuid.New()}
	file1.TagsDecoded = append(file1.TagsDecoded, FileTagName{Name: "Filename 1.txt"})
	file1.TagsDecoded = append(file1.TagsDecoded, FileTagDirectory{Directory: "documents\\sub folder"})

	file2 := BlockRecordFile{Hash: hashData([]byte("Test data 2")), Type: TypeText, Format: FormatText, Size: 9, ID: uuid.New()}
	file2.TagsDecoded = append(file2.TagsDecoded, FileTagName{Name: "Filename 2.txt"})
	file2.TagsDecoded = append(file2.TagsDecoded, FileTagDirectory{Directory: "documents\\sub folder"})

	encodedFiles, _ := encodeBlockRecordFiles([]BlockRecordFile{file1, file2})

	blockE := &Block{BlockchainVersion: 42, Number: 0}
	blockE.RecordsRaw = append(blockE.RecordsRaw, encoded1...)
	blockE.RecordsRaw = append(blockE.RecordsRaw, encodedFiles...)

	raw, err := encodeBlock(blockE, privateKey)
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		return
	}

	block, err := decodeBlock(raw)
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		return
	}

	decoded, err := decodeBlockRecords(block)
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		return
	}

	// output the block details
	fmt.Printf("Block details:\n----------------\nNumber: %d\nVersion: %d\nLast Hash: %s\nPublic Key: %s\n", block.Number, block.BlockchainVersion, hex.EncodeToString(block.LastBlockHash), hex.EncodeToString(block.OwnerPublicKey.SerializeCompressed()))

	for _, decodedR := range decoded.RecordsDecoded {
		if file, ok := decodedR.(BlockRecordFile); ok {
			fmt.Printf("* File          %s\n", file.ID.String())
			fmt.Printf("  Size          %d\n", file.Size)
			fmt.Printf("  Type          %d\n", file.Type)
			fmt.Printf("  Format        %d\n", file.Format)
			fmt.Printf("  Hash          %s\n", hex.EncodeToString(file.Hash))

			for _, decodedT := range file.TagsDecoded {
				switch v := decodedT.(type) {
				case FileTagName:
					fmt.Printf("  Name          %s\n", v.Name)
				case FileTagDirectory:
					fmt.Printf("  Directory     %s\n", v.Directory)
				}
			}
		}
	}
}

func initTestPrivateKey() {
	// use static test key, otherwise tests will be inconsistent (would otherwise fail to open blockchain database)
	privateKeyTestA := "d65da474861d826edd29c1307f1250d79e9dbf84e3a2449022658445c8d8ed63"
	privateKeyB, _ := hex.DecodeString(privateKeyTestA)
	peerPrivateKey, peerPublicKey = btcec.PrivKeyFromBytes(btcec.S256(), privateKeyB)
	nodeID = PublicKey2NodeID(peerPublicKey)

	fmt.Printf("Loaded public key: %s\n", hex.EncodeToString(peerPublicKey.SerializeCompressed()))
}

func TestBlockchainAdd(t *testing.T) {
	initTestPrivateKey()
	initUserBlockchain()

	file1 := BlockRecordFile{Hash: hashData([]byte("Test data")), Type: TypeText, Format: FormatText, Size: 9, ID: uuid.New()}
	file1.TagsDecoded = append(file1.TagsDecoded, FileTagName{Name: "Filename 1.txt"})
	file1.TagsDecoded = append(file1.TagsDecoded, FileTagDirectory{Directory: "documents\\sub folder"})

	newHeight, status := UserBlockchainAddFiles([]BlockRecordFile{file1})

	switch status {
	case 0:
	case 1: // Error previous block not found
		fmt.Printf("Error adding file to blockchain: Previous block not found.\n")
	case 2: // Error block encoding
		fmt.Printf("Error adding file to blockchain: Error block encoding.\n")
	case 3: // Error block record encoding
		fmt.Printf("Error adding file to blockchain: Error block record encoding.\n")
	default:
		fmt.Printf("Error adding file to blockchain: Unknown status %d\n", status)
	}

	if status != 0 {
		return
	}

	fmt.Printf("Success adding files to blockchain. New blockchain height: %d\n", newHeight)
}

func TestBlockchainRead(t *testing.T) {
	initTestPrivateKey()
	initFilters()
	initUserBlockchain()

	blockNumber := uint64(0)

	decoded, status, err := UserBlockchainRead(blockNumber)
	switch status {
	case 0:
	case 1: // Error block not found
		fmt.Printf("Error reading block %d: Block not found.\n", blockNumber)
	case 2: // Error block encoding
		fmt.Printf("Error reading block %d: Block encoding corrupt: %s\n", blockNumber, err.Error())
	case 3: // Error block record encoding
		fmt.Printf("Error reading block %d: Block record encoding corrupt.\n", blockNumber)
	default:
		fmt.Printf("Error reading block %d: Unknown status %d\n", blockNumber, status)
	}

	if status != 0 {
		return
	}

	for _, decodedR := range decoded.RecordsDecoded {
		if file, ok := decodedR.(BlockRecordFile); ok {
			fmt.Printf("* File          %s\n", file.ID.String())
			fmt.Printf("  Size          %d\n", file.Size)
			fmt.Printf("  Type          %d\n", file.Type)
			fmt.Printf("  Format        %d\n", file.Format)
			fmt.Printf("  Hash          %s\n", hex.EncodeToString(file.Hash))

			for _, decodedT := range file.TagsDecoded {
				switch v := decodedT.(type) {
				case FileTagName:
					fmt.Printf("  Name          %s\n", v.Name)
				case FileTagDirectory:
					fmt.Printf("  Directory     %s\n", v.Directory)
				}
			}
		}
	}
}
