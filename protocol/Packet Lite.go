/*
File Name:  Packet Lite.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

The lite packet header is used for encoding data transfer packets. The regular header is too expensive in terms of CPU consumption due to public key signing.
Instead, a simple session ID will identify lite packets. The ID is randomized and only valid during the session.
Unsolicited lite packets are therefore impossible; the receiver must have the ID already whitelisted for the packet to be recognized.

Offset  Size   Info
0       16     ID
16      2      Size of data to follow

*/

package protocol

import (
	"encoding/binary"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// PacketLiteRaw is a decrypted P2P lite packet
type PacketLiteRaw struct {
	ID      uuid.UUID // ID
	Payload []byte    // Payload
	Session *LiteID   // Session info
}

// Minimum packet size of lite packets.
const PacketLiteSizeMin = 16 + 2

// IsPacketLite identifies a lite packet based on its ID. If the ID is not recognized, it fails.
func (router *LiteRouter) IsPacketLite(raw []byte) (isLite bool, err error) {
	if len(raw) < PacketLiteSizeMin {
		return false, errors.New("invalid packet size")
	}

	// Parse the ID and then look it up.
	var id uuid.UUID
	copy(id[:], raw[0:16])

	return router.LookupLiteID(id) != nil, nil
}

// PacketLiteDecode a lite packet. It will identify the lite packet based on its ID. If the ID is not recognized (which is the case for regular Peernet packets), the function fails.
// It does not perform any decryption.
func (router *LiteRouter) PacketLiteDecode(raw []byte) (packet *PacketLiteRaw, err error) {
	if len(raw) < PacketLiteSizeMin {
		return nil, errors.New("invalid packet size")
	}

	// Parse the ID and look it up. It will contain information about the decryption algorithm to use.
	var id uuid.UUID
	copy(id[:], raw[0:16])

	session := router.LookupLiteID(id)
	if session == nil {
		return nil, errors.New("packet ID not found")
	}

	// TODO: Decrypt the data if indicated by the session.

	sizePayload := binary.LittleEndian.Uint16(raw[16 : 16+2])
	if int(sizePayload) > len(raw)-PacketLiteSizeMin { // invalid size field?
		return nil, errors.New("invalid packet size field")
	}

	// Valid packet received, extend expiration.
	session.expires = time.Now().Add(session.timeout)

	return &PacketLiteRaw{Payload: raw[PacketLiteSizeMin:], ID: id, Session: session}, nil
}

// Encodes a lite packet.
func PacketLiteEncode(id uuid.UUID, data []byte) (raw []byte, err error) {
	raw = make([]byte, PacketLiteSizeMin+len(data))

	copy(raw[0:16], id[:])
	binary.LittleEndian.PutUint16(raw[16:16+2], uint16(len(data)))
	copy(raw[PacketLiteSizeMin:], data)

	return raw, nil
}

// ---- Lite packet ID management. This is similar to packet sequences. ----

// LiteRouter keeps track of accepted (expected) packet IDs.
type LiteRouter struct {
	// list of recognized IDs
	ids map[uuid.UUID]*LiteID

	sync.Mutex // synchronized access to the IDs
}

// LiteID contains session information for a bidirectional transfer of data
type LiteID struct {
	ID             uuid.UUID     // ID
	created        time.Time     // When the ID was created.
	expires        time.Time     // When the ID expires. This can be extended on the fly!
	Data           interface{}   // Optional high-level data associated with the ID
	timeout        time.Duration // Timeout for receiving the next message
	invalidateFunc func()        // Called on expiration.
}

// Creates a new manager to keep track of accepted IDs.
func NewLiteRouter() (router *LiteRouter) {
	router = &LiteRouter{
		ids: make(map[uuid.UUID]*LiteID),
	}

	go router.autoDeleteExpired()

	return
}

// autoDeleteExpired deletes all IDs that are expired.
func (router *LiteRouter) autoDeleteExpired() {
	for {
		time.Sleep(4 * time.Second)
		now := time.Now()

		router.Lock()
		for id, info := range router.ids {
			if info.expires.Before(now) {
				delete(router.ids, id)

				if info.invalidateFunc != nil {
					go info.invalidateFunc()
				}
			}
		}
		router.Unlock()
	}
}

func (router *LiteRouter) LookupLiteID(id uuid.UUID) (info *LiteID) {
	router.Lock()
	info = router.ids[id]
	router.Unlock()

	return info
}

// Returns a new lite ID to be used.
func (router *LiteRouter) NewLiteID(data interface{}, timeout time.Duration, invalidateFunc func()) (info *LiteID) {
	info = &LiteID{
		created:        time.Now(),
		expires:        time.Now().Add(timeout),
		timeout:        timeout,
		invalidateFunc: invalidateFunc,
		Data:           data,
		ID:             uuid.New(),
	}

	router.Lock()
	router.ids[info.ID] = info
	router.Unlock()

	return
}

func (router *LiteRouter) RegisterLiteID(id uuid.UUID, data interface{}, timeout time.Duration, invalidateFunc func()) (info *LiteID) {
	info = &LiteID{
		ID:             id,
		created:        time.Now(),
		expires:        time.Now().Add(timeout),
		timeout:        timeout,
		invalidateFunc: invalidateFunc,
		Data:           data,
	}

	router.Lock()
	existingInfo := router.ids[info.ID]
	router.ids[info.ID] = info
	router.Unlock()

	// Call the invalidate function if there is a collision. This should never happen.
	if existingInfo != nil && existingInfo.invalidateFunc != nil {
		go existingInfo.invalidateFunc()
	}

	return
}
