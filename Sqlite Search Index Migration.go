package core

import (
	"encoding/hex"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"log"
)

// InitializeSqliteDB Opens a connection with Sqlite or creates one
// if it does not exist
func InitializeSqliteDB(DBname string) (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open(DBname), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	return db, nil
}

// SearchIndex Structure of the SQLite table for search index
type SearchIndex struct {
	ID      uuid.UUID
	Hash    string
	KeyHash string
}

func SqliteSearchIndexMigration() {
	db, err := InitializeSqliteDB(configFile + "SearchIndex.db")
	if err != nil {
		log.Print(err)
	}
	// Migration of the search index struct
	db.AutoMigrate(SearchIndex{})
}

// InsertIndexRows inserts data of type of SearchIndex
func InsertIndexRows(hashes [][]byte, text string) error {

	db, err := InitializeSqliteDB(configFile + "SearchIndex.db")
	if err != nil {
		return err
	}

	for i := range hashes {
		var hash SearchIndex
		// generating UUID
		newUUID, err := uuid.NewUUID()
		if err != nil {
			return err
		}

		// covert hash to string to append
		// pre text to mention it's part
		// of a string and not a hash
		// prefix: text:<string>
		hash.ID = newUUID
		hash.KeyHash = "text:" + text
		hash.Hash = hex.EncodeToString(hashes[i])

		db.Create(hash)
	}

	return nil
}

// SearchTextBasedOnHash Returns all keys based on
// hash provided
func SearchTextBasedOnHash(hash []byte) ([]string, error) {
	db, err := InitializeSqliteDB(configFile + "SearchIndex.db")
	if err != nil {
		return nil, err
	}

	Result, err := db.Model(&SearchIndex{}).Where("hash = ?", hex.EncodeToString(hash)).Rows()
	if err != nil {
		return nil, err
	}

	defer Result.Close()

	var searchIndex SearchIndex
	var response []string

	for Result.Next() {
		db.ScanRows(Result, &searchIndex)
		response = append(response, searchIndex.KeyHash)
	}

	return response, err
}

func deleteIndexesBasedOnText(db *gorm.DB, text string) error {
	db.Where("key_hash = ?", text).Delete(SearchIndex{})
	return nil
}

// DeleteIndexesBasedOnHash deletes hash references based on hash provided
func DeleteIndexesBasedOnHash(hash []byte) error {
	db, err := InitializeSqliteDB(configFile + "SearchIndex.db")
	if err != nil {
		return err
	}
	// encode hash to string
	hashStr := hex.EncodeToString(hash)
	// get text based on hash provided
	Result, err := db.Model(&SearchIndex{}).Where("hash = ?", hashStr).Rows()
	if err != nil {
		return err
	}

	defer Result.Close()

	var searchIndex SearchIndex

	for Result.Next() {
		db.ScanRows(Result, &searchIndex)
		deleteIndexesBasedOnText(db, searchIndex.KeyHash)
	}

	return nil
}
