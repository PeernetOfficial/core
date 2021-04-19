/*
File Name:  Message Sequence.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Records and verifies message sequences.

Advantages:
* This secures against replay and poisoning attacks.
* If used correctly it can also deduplicate messages (which occurs when 2 peers have multiple registered connections to each other but none are active and subsequent fallback to broadcast).
* The round-trip time can be measured and used to determine the connection quality.
* (future) It can be used to detect missed and lost replies.
*/

package core

import (
	"math/rand"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcd/btcec"
)

// sequences stores all sequence numbers that are valid at the moment. The value represents the time the sequence number was used.
// Key = Peer ID + Sequence Number
var sequences map[string]*sequenceExpiry
var sequencesMutex sync.Mutex

type sequenceExpiry struct {
	created time.Time // When the sequence was created.
	expires time.Time // When the sequence expires. This can be extended on the fly!
	counter int       // How many replies used the sequence. Multiple Response messages may be returned for a single Announcement one.
}

func initMessageSequence() {
	sequences = make(map[string]*sequenceExpiry)

	// auto-delete worker to remove expired sequences
	go func() {
		for {
			time.Sleep(time.Duration(ReplyTimeout) * time.Second)
			now := time.Now()

			sequencesMutex.Lock()
			for key, sequence := range sequences {
				if sequence.expires.Before(now) {
					delete(sequences, key)
				}
			}
			sequencesMutex.Unlock()
		}
	}()
}

// msgNewSequence returns a new sequence and registers is
// Use only for Announcement and Ping messages.
func (peer *PeerInfo) msgNewSequence() (sequence uint32) {
	sequence = atomic.AddUint32(&peer.messageSequence, 1)

	key := string(peer.PublicKey.SerializeCompressed()) + strconv.FormatUint(uint64(sequence), 10)

	// Add the sequence to the list. Sequences are unique enough that collisions are unlikely and negligible.
	sequencesMutex.Lock()
	sequences[key] = &sequenceExpiry{
		created: time.Now(),
		expires: time.Now().Add(time.Duration(ReplyTimeout) * time.Second),
	}
	sequencesMutex.Unlock()

	return sequence
}

// msgArbitrarySequence returns an arbitrary sequence to be used for uncontacted peers
func msgArbitrarySequence(publicKey *btcec.PublicKey) (sequence uint32) {
	sequence = rand.Uint32()

	key := string(publicKey.SerializeCompressed()) + strconv.FormatUint(uint64(sequence), 10)

	// Add the sequence to the list. Sequences are unique enough that collisions are unlikely and negligible.
	sequencesMutex.Lock()
	sequences[key] = &sequenceExpiry{
		created: time.Now(),
		expires: time.Now().Add(time.Duration(ReplyTimeout) * time.Second),
	}
	sequencesMutex.Unlock()

	return sequence
}

// msgValidateSequence validates the sequence number of an incoming message
func msgValidateSequence(raw *MessageRaw) (valid bool, rtt time.Duration) {
	// Only Response and Pong
	if raw.Command != CommandResponse && raw.Command != CommandPong {
		return true, rtt
	}

	key := string(raw.SenderPublicKey.SerializeCompressed()) + strconv.FormatUint(uint64(raw.Sequence), 10)

	sequencesMutex.Lock()
	defer sequencesMutex.Unlock()

	// lookup the sequence
	sequence, ok := sequences[key]
	if !ok {
		return false, rtt
	}

	// Initial reply: Store latest roundtrip time. That value might be distorted on Response vs Pong since Response messages might send data
	// up to 64 KB which obviously would be transmitted slower than an empty Pong reply. However, for the real world this is good enough.
	if sequence.counter == 0 {
		rtt = time.Since(sequence.created)
	}

	sequence.counter++

	// Special case CommandResponse: Extend validity in case there are follow-up responses by half of the round-trip time since they will be sent one-way.
	if raw.Command == CommandResponse {
		sequence.expires = time.Now().Add(time.Duration(ReplyTimeout) * time.Second / 2)
	}

	return sequence.expires.After(time.Now()), rtt
}

// msgInvalidateSequence invalidates the sequence number.
func msgInvalidateSequence(raw *MessageRaw) {
	// Only Response
	if raw.Command != CommandResponse {
		return
	}

	key := string(raw.SenderPublicKey.SerializeCompressed()) + strconv.FormatUint(uint64(raw.Sequence), 10)

	sequencesMutex.Lock()
	delete(sequences, key)
	sequencesMutex.Unlock()
}
