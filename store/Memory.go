/*
File Name:  Memory.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package store

import (
	"sync"
	"time"
)

// MemoryStore is a simple in-memory key/value store for testing purposes.
type MemoryStore struct {
	mutex     *sync.Mutex
	data      map[string][]byte
	expireMap map[string]time.Time
}

// NewMemoryStore create a properly initialized memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		data:      make(map[string][]byte),
		mutex:     &sync.Mutex{},
		expireMap: make(map[string]time.Time),
	}
}

// ExpireKeys is called to delete all keys that are marked for expiration.
func (ms *MemoryStore) ExpireKeys() {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	for k, v := range ms.expireMap {
		if time.Now().After(v) {
			delete(ms.expireMap, k)
			delete(ms.data, k)
		}
	}
}

// Store stores the key/value pair.
func (ms *MemoryStore) Store(key []byte, data []byte) error {
	ms.mutex.Lock()
	ms.data[string(key)] = data
	ms.mutex.Unlock()
	return nil
}

// StoreExpire stores the key/value pair and deletes it after the expiration time.
func (ms *MemoryStore) StoreExpire(key []byte, data []byte, expiration time.Time) error {
	ms.mutex.Lock()
	ms.expireMap[string(key)] = expiration
	ms.data[string(key)] = data
	ms.mutex.Unlock()
	return nil
}

// Retrieve returns the value for the key if present.
func (ms *MemoryStore) Retrieve(key []byte) (data []byte, found bool) {
	ms.mutex.Lock()
	data, found = ms.data[string(key)]
	ms.mutex.Unlock()
	return data, found
}

// Delete deletes a key/value pair.
func (ms *MemoryStore) Delete(key []byte) {
	ms.mutex.Lock()
	delete(ms.expireMap, string(key))
	delete(ms.data, string(key))
	ms.mutex.Unlock()
}
