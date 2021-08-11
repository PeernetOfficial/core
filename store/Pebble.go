/*
File Name:  Pebble.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Note: It turned out that pebble has many dependencies and increases the binary file size by ~6 MB.
*/

package store

/*
import (
	"errors"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
)

// PebbleStore is a key/value store using Pebble from CockroachDB.
// Expiration is currently not supported.
type PebbleStore struct {
	mutex    *sync.Mutex
	filename string
	db       *pebble.DB
}

// NewPebbleStore create a properly initialized pebble store.
func NewPebbleStore(filename string) (store *PebbleStore, err error) {
	// if the database does not exist, it will be created
	db, err := pebble.Open(filename, &pebble.Options{})
	if err != nil {
		return nil, err
	}

	return &PebbleStore{
		mutex:    &sync.Mutex{},
		filename: filename,
		db:       db,
	}, nil
}

func (store *PebbleStore) ExpireKeys() {
	// Not yet implemented
}

// Store stores the key/value pair.
func (store *PebbleStore) Set(key []byte, data []byte) error {
	return store.db.Set(key, data, pebble.Sync)
}

// StoreExpire stores the key/value pair and deletes it after the expiration time.
func (store *PebbleStore) StoreExpire(key []byte, data []byte, expiration time.Time) error {
	// Not yet implemented
	return errors.New("not yet implemented")
}

// Get returns the value for the key if present.
func (store *PebbleStore) Get(key []byte) (data []byte, found bool) {
	value, closer, err := store.db.Get(key)
	if err != nil {
		return nil, false
	}
	closer.Close()
	return value, true
}

// Delete deletes a key/value pair.
func (store *PebbleStore) Delete(key []byte) {
	store.db.Delete(key, pebble.Sync)
}
*/
