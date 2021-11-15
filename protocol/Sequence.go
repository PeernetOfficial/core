/*
File Name:  Sequence.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

This code caches and verifies message sequences. Sequence numbers are valid on a peer level, independent of which network connection was used.
They can be used to map incoming response messages to previous outgoing requests. The remote peer ID is used together with a consecutive sequence number as unique key.

Sequences are created for:
* Unidirectional messages (responses are one-way)
* Bidirectional messages (responses may be sent back and forth). They use a different key namespace since remote sequence numbers could collide with locally created ones.

Advantages:
* This secures against replay and poisoning attacks.
* If used correctly it can also deduplicate messages (which occurs when 2 peers have multiple registered connections to each other but none are active and subsequent fallback to broadcast).
* The round-trip time can be measured and used to determine the connection quality.
* (future) It can be used to detect missed and lost replies.

*/

package protocol

import (
	"math/rand"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PeernetOfficial/core/btcec"
)

// SequenceManager stores all message sequence numbers that are valid at the moment
type SequenceManager struct {
	ReplyTimeout int // The round-trip timeout for message sequences.

	// sequences is the list of sequence numbers that are valid at the moment. The value represents the time the sequence number.
	// Key = Peer ID + Sequence Number
	sequences map[string]*SequenceExpiry

	sync.Mutex // synchronized access to the sequences
}

// SequenceExpiry contains the decoded sequence information of a message.
type SequenceExpiry struct {
	SequenceNumber uint32      // Sequence number
	created        time.Time   // When the sequence was created.
	expires        time.Time   // When the sequence expires. This can be extended on the fly!
	counter        int         // How many replies used the sequence. Multiple Response messages may be returned for a single Announcement one.
	Data           interface{} // Optional high-level data associated with the sequence
	// bidirectional sequences only
	bidirectional  bool          // Whether this sequence is used in a bidirectional way
	timeout        time.Duration // Timeout for receiving the next message
	invalidateFunc func()        // The invalidation callback is in case a sequence collision or expiration invalidates the sequence.
}

// NewSequenceManager creates a new sequence manager. The ReplyTimeout is in seconds. The expiration function is started immediately.
func NewSequenceManager(ReplyTimeout int) (manager *SequenceManager) {
	manager = &SequenceManager{
		ReplyTimeout: ReplyTimeout,
		sequences:    make(map[string]*SequenceExpiry),
	}

	go manager.autoDeleteExpired()

	return
}

// autoDeleteExpired deletes all sequences that are expired.
func (manager *SequenceManager) autoDeleteExpired() {
	for {
		time.Sleep(time.Duration(manager.ReplyTimeout) * time.Second)
		now := time.Now()

		manager.Lock()
		for key, sequence := range manager.sequences {
			if sequence.expires.Before(now) {
				delete(manager.sequences, key)

				if sequence.invalidateFunc != nil {
					go sequence.invalidateFunc()
				}
			}
		}
		manager.Unlock()
	}
}

// sequence2Key creates the lookup key of a sequence for a peer.
// Since bidirectional sequence numbers may be created from either side (remote or local peer), it does not share a namespace with unidirectional sequence numbers.
func sequence2Key(bidirectional bool, publicKey *btcec.PublicKey, sequenceNumber uint32) (key string) {
	if !bidirectional {
		return "u" + string(publicKey.SerializeCompressed()) + strconv.FormatUint(uint64(sequenceNumber), 10)
	} else {
		return "b" + string(publicKey.SerializeCompressed()) + strconv.FormatUint(uint64(sequenceNumber), 10)
	}
}

// NewSequence returns a new sequence and registers it. messageSequence must point to the variable holding the continuous next sequence number.
// Use only for Announcement and Ping messages.
func (manager *SequenceManager) NewSequence(publicKey *btcec.PublicKey, messageSequence *uint32, data interface{}) (info *SequenceExpiry) {
	info = &SequenceExpiry{
		SequenceNumber: atomic.AddUint32(messageSequence, 1),
		created:        time.Now(),
		expires:        time.Now().Add(time.Duration(manager.ReplyTimeout) * time.Second),
		Data:           data,
	}

	// Add the sequence to the list. Sequences are unique enough that collisions are unlikely and negligible.
	key := sequence2Key(false, publicKey, info.SequenceNumber)
	manager.Lock()
	manager.sequences[key] = info
	manager.Unlock()

	return
}

// ArbitrarySequence returns an arbitrary sequence to be used for uncontacted peers
func (manager *SequenceManager) ArbitrarySequence(publicKey *btcec.PublicKey, data interface{}) (info *SequenceExpiry) {
	info = &SequenceExpiry{
		SequenceNumber: rand.Uint32(),
		created:        time.Now(),
		expires:        time.Now().Add(time.Duration(manager.ReplyTimeout) * time.Second),
		Data:           data,
	}

	// Add the sequence to the list. Sequences are unique enough that collisions are unlikely and negligible.
	key := sequence2Key(false, publicKey, info.SequenceNumber)
	manager.Lock()
	manager.sequences[key] = info
	manager.Unlock()

	return
}

// ValidateSequence validates the sequence number of an incoming message. It will set raw.sequence if valid.
func (manager *SequenceManager) ValidateSequence(publicKey *btcec.PublicKey, sequenceNumber uint32, invalidate, extendValidity bool) (sequenceInfo *SequenceExpiry, valid bool, rtt time.Duration) {
	key := sequence2Key(false, publicKey, sequenceNumber)

	manager.Lock()
	defer manager.Unlock()

	// lookup the sequence
	sequence, ok := manager.sequences[key]
	if !ok {
		return nil, false, rtt
	}

	// Initial reply: Store latest roundtrip time. That value might be distorted on Response vs Pong since Response messages might send data
	// up to 64 KB which obviously would be transmitted slower than an empty Pong reply. However, for the real world this is good enough.
	if sequence.counter == 0 {
		rtt = time.Since(sequence.created)
	}

	sequence.counter++

	// invalidate the sequence immediately?
	if invalidate {
		delete(manager.sequences, key)
	} else if extendValidity {
		// Special case CommandResponse: Extend validity in case there are follow-up responses, by half of the round-trip time since they will be sent one-way.
		sequence.expires = time.Now().Add(time.Duration(manager.ReplyTimeout) * time.Second / 2)
	}

	return sequence, sequence.expires.After(time.Now()), rtt
}

// InvalidateSequence invalidates the sequence number. It does not call invalidateFunc.
func (manager *SequenceManager) InvalidateSequence(publicKey *btcec.PublicKey, sequenceNumber uint32, bidirectional bool) {
	key := sequence2Key(bidirectional, publicKey, sequenceNumber)

	manager.Lock()
	delete(manager.sequences, key)
	manager.Unlock()
}

// ---- bidirectional sequences ----

// RegisterSequenceBi registers a bidirectional sequence initiated by a remote peer. The caller must specify the timeout (which will be reset every time a new message appears in this sequence).
// This is needed for bidirectional responses to accept subsequent incoming messages from the remote peer.
func (manager *SequenceManager) RegisterSequenceBi(publicKey *btcec.PublicKey, sequenceNumber uint32, data interface{}, timeout time.Duration, invalidateFunc func()) (info *SequenceExpiry) {
	info = &SequenceExpiry{
		SequenceNumber: sequenceNumber,
		created:        time.Now(),
		expires:        time.Now().Add(timeout),
		timeout:        timeout,
		invalidateFunc: invalidateFunc,
		Data:           data,
	}

	// Before registering the sequence, check if there is a collision. If yes, invalidate the original one.
	key := sequence2Key(true, publicKey, info.SequenceNumber)
	manager.Lock()
	existingSequence := manager.sequences[key]
	manager.sequences[key] = info
	manager.Unlock()

	// Call the invalidate function if there is a collision.
	if existingSequence != nil && existingSequence.invalidateFunc != nil {
		go existingSequence.invalidateFunc()
	}

	return
}

// NewSequenceBi returns a new bidirectional sequence and registers it. messageSequence must point to the variable holding the continuous next sequence number.
func (manager *SequenceManager) NewSequenceBi(publicKey *btcec.PublicKey, messageSequence *uint32, data interface{}, timeout time.Duration, invalidateFunc func()) (info *SequenceExpiry) {
	info = &SequenceExpiry{
		created:        time.Now(),
		expires:        time.Now().Add(timeout),
		bidirectional:  true,
		timeout:        timeout,
		invalidateFunc: invalidateFunc,
		Data:           data,
	}

	manager.Lock()
	defer manager.Unlock()

	// The likelihood of a collision is low but not impossible.
	for n := 0; n < 10000; n++ {
		info.SequenceNumber = atomic.AddUint32(messageSequence, 1)

		key := sequence2Key(true, publicKey, info.SequenceNumber)
		if infoE := manager.sequences[key]; infoE == nil {
			manager.sequences[key] = info
			return info
		}
	}

	return nil
}

// ValidateSequenceBi validates the sequence number of an incoming message. It will set raw.sequence if valid.
func (manager *SequenceManager) ValidateSequenceBi(publicKey *btcec.PublicKey, sequenceNumber uint32, isLast bool) (sequenceInfo *SequenceExpiry, valid bool, rtt time.Duration) {
	key := sequence2Key(true, publicKey, sequenceNumber)

	manager.Lock()
	defer manager.Unlock()

	// lookup the sequence
	sequence, ok := manager.sequences[key]
	if !ok {
		return nil, false, rtt
	}

	// Initial reply: Store latest roundtrip time.
	if sequence.counter == 0 {
		rtt = time.Since(sequence.created)
	}

	sequence.counter++

	// invalidate the sequence immediately?
	if isLast {
		delete(manager.sequences, key)
	} else {
		sequence.expires = time.Now().Add(sequence.timeout)
	}

	return sequence, sequence.expires.After(time.Now()), rtt
}
