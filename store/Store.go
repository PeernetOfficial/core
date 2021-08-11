/*
File Name:  Store.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Simple key-value store implementation. Currently in-memory only.
*/

package store

import (
	"time"
)

// Store is the interface for implementing the storage mechanism for the DHT.
type Store interface {
	// Store stores the key/value pair.
	Store(key []byte, data []byte) error

	// StoreExpire stores the key/value pair and deletes it after the expiration time.
	// If key/value already exists, it will be overwritten and the new expiration time applies.
	StoreExpire(key []byte, data []byte, expiration time.Time) error

	// Retrieve returns the value for the key if present.
	Retrieve(key []byte) (data []byte, found bool)

	// Delete deletes a key/value pair.
	Delete(key []byte)

	// ExpireKeys is called to delete all keys that are marked for expiration.
	ExpireKeys()
}
