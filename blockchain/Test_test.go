package blockchain

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/PeernetOfficial/core/btcec"
	"github.com/PeernetOfficial/core/merkle"
	"github.com/PeernetOfficial/core/protocol"
	"github.com/google/uuid"
)

func TestBlockEncoding(t *testing.T) {
	privateKey, err := btcec.NewPrivateKey(btcec.S256())
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		return
	}

	encoded1, _ := encodeBlockRecordProfile([]BlockRecordProfile{ProfileFieldFromText(ProfileName, "Test User 1")})

	file1, _ := createBlockRecordFile([]byte("Test data"), "Filename 1.txt", "documents\\sub folder")
	file2, _ := createBlockRecordFile([]byte("Test data 2!"), "Filename 2.txt", "documents\\sub folder")

	encodedFiles, err := encodeBlockRecordFiles([]BlockRecordFile{file1, file2})
	if err != nil {
		fmt.Printf("Error encoding files: %s\n", err.Error())
		return
	}

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
		switch record := decodedR.(type) {
		case BlockRecordFile:
			printFile(record)

		case BlockRecordProfile:
			printProfileField(record)

		}

	}
}

func createBlockRecordFile(data []byte, name, folder string) (file BlockRecordFile, err error) {
	file = BlockRecordFile{Hash: protocol.HashData(data), Type: testTypeText, Format: testFormatText, Size: uint64(len(data)), ID: uuid.New()}
	file.Tags = append(file.Tags, TagFromText(TagName, name))
	file.Tags = append(file.Tags, TagFromText(TagFolder, folder))

	file.FragmentSize = merkle.CalculateFragmentSize(file.Size)
	tree, err := merkle.NewMerkleTree(file.Size, file.FragmentSize, bytes.NewBuffer(data))
	if err != nil {
		return file, err
	}
	file.MerkleRootHash = tree.RootHash

	return file, nil
}

func initTestPrivateKey() (blockchain *Blockchain, err error) {
	// use static test key, otherwise tests will be inconsistent (would otherwise fail to open blockchain database)
	privateKeyTestA := "d65da474861d826edd29c1307f1250d79e9dbf84e3a2449022658445c8d8ed63"
	privateKeyB, _ := hex.DecodeString(privateKeyTestA)
	peerPrivateKey, peerPublicKey := btcec.PrivKeyFromBytes(btcec.S256(), privateKeyB)

	fmt.Printf("Loaded public key: %s\n", hex.EncodeToString(peerPublicKey.SerializeCompressed()))

	return Init(peerPrivateKey, "test.blockchain")
}

func TestBlockchainAdd(t *testing.T) {
	blockchain, err := initTestPrivateKey()
	if err != nil {
		return
	}

	file1, _ := createBlockRecordFile([]byte("Test data"), "Filename 1.txt", "documents\\sub folder")

	newHeight, newVersion, status := blockchain.AddFiles([]BlockRecordFile{file1})

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

	fmt.Printf("Success adding files to blockchain. New blockchain height %d version %d\n", newHeight, newVersion)
}

func TestBlockchainRead(t *testing.T) {
	blockchain, err := initTestPrivateKey()
	if err != nil {
		return
	}

	blockNumber := uint64(0)

	decoded, status, err := blockchain.Read(blockNumber)
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
			printFile(file)
		}
	}
}

func printFile(file BlockRecordFile) {
	fmt.Printf("* File                %s\n", file.ID.String())
	fmt.Printf("  Size                %d\n", file.Size)
	fmt.Printf("  Type                %d\n", file.Type)
	fmt.Printf("  Format              %d\n", file.Format)
	fmt.Printf("  Hash                %s\n", hex.EncodeToString(file.Hash))
	fmt.Printf("  Merkle Root Hash    %s\n", hex.EncodeToString(file.MerkleRootHash))
	fmt.Printf("  Fragment Size       %d\n", file.FragmentSize)

	for _, tag := range file.Tags {
		switch tag.Type {
		case TagName:
			fmt.Printf("  Name                %s\n", tag.Text())
		case TagFolder:
			fmt.Printf("  Folder              %s\n", tag.Text())
		case TagDescription:
			fmt.Printf("  Description         %s\n", tag.Text())
		}
	}
}

func TestBlockchainDelete(t *testing.T) {
	blockchain, err := initTestPrivateKey()
	if err != nil {
		return
	}

	// test add file
	file1, _ := createBlockRecordFile([]byte("Test data"), "Test file to be deleted.txt", "documents\\sub folder")

	newHeight, newVersion, status := blockchain.AddFiles([]BlockRecordFile{file1})
	fmt.Printf("Added file: Status %d height %d version %d\n", status, newHeight, newVersion)

	// list files
	files, _ := blockchain.ListFiles()
	for _, file := range files {
		printFile(file)
	}

	fmt.Printf("----------------\n")

	// delete the file
	newHeight, newVersion, _, status = blockchain.DeleteFiles([]uuid.UUID{file1.ID})
	fmt.Printf("Deleted file: Status %d height %d version %d\n", status, newHeight, newVersion)

	// list all files
	files, _ = blockchain.ListFiles()
	for _, file := range files {
		printFile(file)
	}
}

func TestBlockchainProfile(t *testing.T) {
	blockchain, err := initTestPrivateKey()
	if err != nil {
		return
	}

	// write some test profile data
	newHeight, newVersion, status := blockchain.ProfileWrite([]BlockRecordProfile{
		ProfileFieldFromText(ProfileName, "Test User 1"),
		ProfileFieldFromText(ProfileEmail, "test@test.com"),
		{Type: 100, Data: []byte{0, 1, 2, 3}}})

	fmt.Printf("Write profile data: Status %d height %d version %d\n", status, newHeight, newVersion)

	// list all profile info
	printProfileData(blockchain)

	fmt.Printf("----------------\n")

	// delete profile info
	newHeight, newVersion, status = blockchain.ProfileDelete([]uint16{ProfileEmail})
	fmt.Printf("Deleted profile email: Status %d height %d version %d\n", status, newHeight, newVersion)

	printProfileData(blockchain)
}

func printProfileData(blockchain *Blockchain) {
	fields, status := blockchain.ProfileList()
	if status != StatusOK {
		fmt.Printf("Reading profile data error status: %d\n", status)
		return
	}

	if len(fields) == 0 {
		fmt.Printf("No profile data to visualize.\n")
		return
	}

	for _, field := range fields {
		printProfileField(field)
	}
}

func printProfileField(field BlockRecordProfile) {
	switch field.Type {
	case ProfileName, ProfileEmail, ProfileWebsite, ProfileTwitter, ProfileYouTube, ProfileAddress:
		fmt.Printf("* Field  %d  =  %s\n", field.Type, string(field.Data))

	default:
		fmt.Printf("* Field  %d  =  %s\n", field.Type, hex.EncodeToString(field.Data))
	}
}

const testTypeText = 1
const testFormatText = 10
