package blockchain

import (
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/btcec"
	"github.com/google/uuid"
	"lukechampine.com/blake3"
)

func TestBlockEncoding(t *testing.T) {
	initRequiredFunctions()
	privateKey, err := btcec.NewPrivateKey(btcec.S256())
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		return
	}

	encoded1, _ := encodeBlockRecordProfile([]BlockRecordProfile{ProfileFieldFromText(ProfileName, "Test User 1")})

	file1 := BlockRecordFile{Hash: testHashData([]byte("Test data")), Type: testTypeText, Format: testFormatText, Size: 9, ID: uuid.New()}
	file1.Tags = append(file1.Tags, TagFromText(TagName, "Filename 1.txt"))
	file1.Tags = append(file1.Tags, TagFromText(TagFolder, "documents\\sub folder"))

	file2 := BlockRecordFile{Hash: testHashData([]byte("Test data 2!")), Type: testTypeText, Format: testFormatText, Size: 10, ID: uuid.New()}
	file2.Tags = append(file2.Tags, TagFromText(TagName, "Filename 2.txt"))
	file2.Tags = append(file2.Tags, TagFromText(TagFolder, "documents\\sub folder"))

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
		switch record := decodedR.(type) {
		case BlockRecordFile:
			printFile(record)

		case BlockRecordProfile:
			printProfileField(record)

		}

	}
}

func initRequiredFunctions() {
	HashFunction = testHashData
	PublicKey2NodeID = func(publicKey *btcec.PublicKey) (nodeID []byte) {
		return testHashData(publicKey.SerializeCompressed())
	}
}

func initTestPrivateKey() (blockchain *Blockchain, err error) {
	// use static test key, otherwise tests will be inconsistent (would otherwise fail to open blockchain database)
	privateKeyTestA := "d65da474861d826edd29c1307f1250d79e9dbf84e3a2449022658445c8d8ed63"
	privateKeyB, _ := hex.DecodeString(privateKeyTestA)
	peerPrivateKey, peerPublicKey := btcec.PrivKeyFromBytes(btcec.S256(), privateKeyB)

	fmt.Printf("Loaded public key: %s\n", hex.EncodeToString(peerPublicKey.SerializeCompressed()))

	initRequiredFunctions()

	return Init(peerPrivateKey, "test.blockchain")
}

func TestBlockchainAdd(t *testing.T) {
	blockchain, err := initTestPrivateKey()
	if err != nil {
		return
	}

	file1 := BlockRecordFile{Hash: testHashData([]byte("Test data")), Type: testTypeText, Format: testFormatText, Size: 9, ID: uuid.New()}
	file1.Tags = append(file1.Tags, TagFromText(TagName, "Filename 1.txt"))
	file1.Tags = append(file1.Tags, TagFromText(TagFolder, "documents\\sub folder"))

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
	fmt.Printf("* File          %s\n", file.ID.String())
	fmt.Printf("  Size          %d\n", file.Size)
	fmt.Printf("  Type          %d\n", file.Type)
	fmt.Printf("  Format        %d\n", file.Format)
	fmt.Printf("  Hash          %s\n", hex.EncodeToString(file.Hash))

	for _, tag := range file.Tags {
		switch tag.Type {
		case TagName:
			fmt.Printf("  Name          %s\n", tag.Text())
		case TagFolder:
			fmt.Printf("  Folder        %s\n", tag.Text())
		case TagDescription:
			fmt.Printf("  Description   %s\n", tag.Text())
		}
	}
}

func TestBlockchainDelete(t *testing.T) {
	blockchain, err := initTestPrivateKey()
	if err != nil {
		return
	}

	// test add file
	file1 := BlockRecordFile{Hash: testHashData([]byte("Test data")), Type: testTypeText, Format: testFormatText, Size: 9, ID: uuid.New()}
	file1.Tags = append(file1.Tags, TagFromText(TagName, "Test file to be deleted.txt"))
	file1.Tags = append(file1.Tags, TagFromText(TagFolder, "documents\\sub folder"))

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

func testHashData(data []byte) (hash []byte) {
	hash32 := blake3.Sum256(data)
	return hash32[:]
}

const testTypeText = 1
const testFormatText = 10
