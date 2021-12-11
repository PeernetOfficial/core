/*
File Name:  Multi.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Multi-blockchain store implementation.

Keys used in the key-value store:
1. Key: Public key compressed, Value: Header
2. Key: Public key compressed + version + block number, Value: Block

*/

package blockchain

import (
	"encoding/binary"
	"errors"
	"time"

	"github.com/PeernetOfficial/core/btcec"
	"github.com/PeernetOfficial/core/store"
)

const (
	MultiStatusOK              = 0 // Success.
	MultiStatusErrorReadHeader = 1 // Error reading header for blockchain.
	MultiStatusHeaderNA        = 2 // Header not available. This indicates nothing is stored for the blockchain.
	MultiStatusInvalidRemote   = 3 // Invalid remote reported blockchain version and height. (local cache is "newer" which should not happen)
	MultiStatusEqual           = 4 // Remote blockchain and local cache are equal.
	MultiStatusNewVersion      = 5 // Local cache is out of date. A new version of the blockchain is available.
	MultiStatusNewBlocks       = 6 // Local cache is out of date. Additional blocks are available.
)

// MultiStore stores multiple blockchains.
type MultiStore struct {
	path     string      // Path of the blockchain on disk. Depends on key-value store whether a filename or folder.
	Database store.Store // The database storing the blockchain.
}

func InitMultiStore(path string) (multi *MultiStore, err error) {
	multi = &MultiStore{path: path}

	// open existing blockchain file or create new one
	if multi.Database, err = store.NewPogrebStore(path); err != nil {
		return nil, err
	}

	return multi, nil
}

/*
Header for blockchains:

Offset  Size   Info
0       8      Version of the blockchain
8       8      Height of the blockchain
16      8      Count of blocks
24      8      Date first block added
32      8      Date last block added
40      8 * n  List of block numbers that are stored

*/

// This is a header for a single blockchain stored in a multi store.
type MultiBlockchainHeader struct {
	Height              uint64    // Height is exchanged as uint32 in the protocol, but stored as uint64.
	Version             uint64    // Version is always uint64.
	DateFirstBlockAdded time.Time // Date the first block was added
	DateLastBlockAdded  time.Time // Date the last block was added
	ListBlocks          []uint64  // List of block numbers that are stored
}

// Reads a blockchains header if available.
func (multi *MultiStore) ReadBlockchainHeader(publicKey *btcec.PublicKey) (header *MultiBlockchainHeader, found bool, err error) {
	buffer, found := multi.Database.Get(publicKey.SerializeCompressed())
	if !found {
		return nil, false, nil
	} else if len(buffer) < 40 {
		return nil, false, errors.New("header length too small")
	}

	header = &MultiBlockchainHeader{}
	header.Version = binary.LittleEndian.Uint64(buffer[0:8])
	header.Height = binary.LittleEndian.Uint64(buffer[8:16])
	countBlocks := binary.LittleEndian.Uint64(buffer[16:24])
	header.DateFirstBlockAdded = time.Unix(int64(binary.LittleEndian.Uint64(buffer[24:32])), 0)
	header.DateLastBlockAdded = time.Unix(int64(binary.LittleEndian.Uint64(buffer[32:40])), 0)

	if uint64(len(buffer)) < 40+8*countBlocks {
		return nil, false, errors.New("header length too small")
	}

	index := 40

	for n := uint64(0); n < countBlocks; n++ {
		blockN := binary.LittleEndian.Uint64(buffer[index : index+8])
		header.ListBlocks = append(header.ListBlocks, blockN)
		index += 8
	}

	return header, true, nil
}

// WriteBlockchainHeader writes a blockchain header. If one exists, it will be overwritten.
func (multi *MultiStore) WriteBlockchainHeader(publicKey *btcec.PublicKey, header *MultiBlockchainHeader) (err error) {
	raw := make([]byte, 40+8*len(header.ListBlocks))

	binary.LittleEndian.PutUint64(raw[0:8], header.Version)
	binary.LittleEndian.PutUint64(raw[8:16], header.Height)
	binary.LittleEndian.PutUint64(raw[16:24], uint64(len(header.ListBlocks)))
	binary.LittleEndian.PutUint64(raw[24:32], uint64(header.DateFirstBlockAdded.UTC().Unix()))
	binary.LittleEndian.PutUint64(raw[32:40], uint64(header.DateLastBlockAdded.UTC().Unix()))

	index := 40

	for _, blockN := range header.ListBlocks {
		binary.LittleEndian.PutUint64(raw[index:index+8], blockN)
		index += 8
	}

	return multi.Database.Set(publicKey.SerializeCompressed(), raw)
}

func lookupKeyForBlock(publicKey *btcec.PublicKey, version, blockNumber uint64) (key []byte) {
	var buffer [16]byte
	binary.LittleEndian.PutUint64(buffer[0:8], version)
	binary.LittleEndian.PutUint64(buffer[8:16], blockNumber)

	key = append(key, publicKey.SerializeCompressed()...)
	key = append(key, buffer[:]...)

	return key
}

// ReadBlock reads a raw block
func (multi *MultiStore) ReadBlock(publicKey *btcec.PublicKey, version, blockNumber uint64) (raw []byte, found bool) {
	return multi.Database.Get(lookupKeyForBlock(publicKey, version, blockNumber))
}

// WriteBlock writes a raw block. It does not update the blockchains header.
func (multi *MultiStore) WriteBlock(publicKey *btcec.PublicKey, version, blockNumber uint64, raw []byte) (err error) {
	return multi.Database.Set(lookupKeyForBlock(publicKey, version, blockNumber), raw)
}

// AssessBlockchainHeader reads the blockchain header, if available, and assesses the status.
func (multi *MultiStore) AssessBlockchainHeader(publicKey *btcec.PublicKey, version, height uint64) (header *MultiBlockchainHeader, status int, err error) {
	// check if there is an existing header for the blockchain
	header, found, err := multi.ReadBlockchainHeader(publicKey)
	if err != nil {
		return nil, MultiStatusErrorReadHeader, err
	} else if !found {
		return nil, MultiStatusHeaderNA, nil
	}

	if header.Version == version && header.Height == height {
		return header, MultiStatusEqual, nil
	}

	// Check if existing version is newer than reported - indicates illegal behavior by the remote peer.
	// Improper refactoring could happen if the local blockchain folder is deleted and automatically recreated.
	if header.Version > version || (header.Version == version && header.Height > height) {
		return header, MultiStatusInvalidRemote, nil
	}

	if version > header.Version {
		return header, MultiStatusNewVersion, nil
	}

	return header, MultiStatusNewBlocks, nil
}

// Deletes a blockchain
func (multi *MultiStore) DeleteBlockchain(publicKey *btcec.PublicKey, header *MultiBlockchainHeader) {
	// first delete all blocks
	for _, blockN := range header.ListBlocks {
		multi.Database.Delete(lookupKeyForBlock(publicKey, header.Version, blockN))
	}

	// delete the header
	multi.Database.Delete(publicKey.SerializeCompressed())
}

func (multi *MultiStore) NewBlockchainHeader(publicKey *btcec.PublicKey, version, height uint64) (header *MultiBlockchainHeader, err error) {
	timeN := time.Now().UTC()
	header = &MultiBlockchainHeader{
		Height:              height,
		Version:             version,
		DateFirstBlockAdded: timeN,
		DateLastBlockAdded:  timeN,
	}

	return header, multi.WriteBlockchainHeader(publicKey, header)
}
