/*
File Name:  Pogreb.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package store

import (
	"errors"
	"io"
	"log"
	"sync"
	"time"

	"github.com/akrylysov/pogreb"
)

// PogrebStore is a key/value store using Pogreb.
// Expiration is currently not supported.
type PogrebStore struct {
	mutex    *sync.Mutex
	filename string
	db       *pogreb.DB
}

// NewPogrebStore create a properly initialized Pogreb store.
func NewPogrebStore(filename string) (store *PogrebStore, err error) {
	pogreb.SetLogger(log.New(io.Discard, "", 0))

	// if the database does not exist, it will be created
	db, err := pogreb.Open(filename, nil)
	if err != nil {
		return nil, err
	}

	return &PogrebStore{
		mutex:    &sync.Mutex{},
		filename: filename,
		db:       db,
	}, nil
}

func (store *PogrebStore) ExpireKeys() {
	// Not yet implemented
}

// Store stores the key/value pair.
func (store *PogrebStore) Set(key []byte, data []byte) error {
	return store.db.Put(key, data)
}

// StoreExpire stores the key/value pair and deletes it after the expiration time.
func (store *PogrebStore) StoreExpire(key []byte, data []byte, expiration time.Time) error {
	// Not yet implemented
	return errors.New("not yet implemented")
}

// Get returns the value for the key if present.
func (store *PogrebStore) Get(key []byte) (data []byte, found bool) {
	value, err := store.db.Get(key)
	if err != nil || value == nil {
		return nil, false
	}
	return value, true
}

// Delete deletes a key/value pair.
func (store *PogrebStore) Delete(key []byte) {
	store.db.Delete(key)
}
