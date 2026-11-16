/*
File Name:  Message Encoding Local Discovery_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package protocol

import (
	"bytes"
	"crypto/ed25519"
	"testing"
)

// TestLocalDiscoverySignature checks the payload layout of the Local Discovery message (announcement, 64 bytes signature) and that the signature
// is bound to the sender, the fixed public key and every byte of the message.
func TestLocalDiscoverySignature(t *testing.T) {
	senderPublic, senderPrivate := generateKey(t)
	otherPublic, otherPrivate := generateKey(t)
	fixedPublic, _ := generateKey(t)
	fixedOtherPublic, _ := generateKey(t)

	announcement := EncodeAnnouncement(true, nil, 1<<FeatureIPv4Listen, 5, 6, "test")[0]

	payload := LocalDiscoverySign(announcement, senderPrivate, fixedPublic)

	if len(payload) != len(announcement)+64 || !bytes.Equal(payload[:len(announcement)], announcement) {
		t.Fatalf("payload of %d bytes is not the announcement of %d bytes followed by 64 bytes", len(payload), len(announcement))
	}
	if !ed25519.Verify(senderPublic, append(append([]byte{}, fixedPublic...), announcement...), payload[len(announcement):]) {
		t.Fatal("signature is not over the fixed public key followed by the announcement")
	}

	verified, err := LocalDiscoveryVerify(payload, senderPublic, fixedPublic)
	if err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(verified, announcement) {
		t.Fatal("returned announcement mismatch")
	}

	decoded, err := DecodeAnnouncement(&MessageRaw{PacketRaw: PacketRaw{Command: CommandLocalDiscovery, Payload: verified}, SenderPublicKey: senderPublic})
	if err != nil {
		t.Fatal(err)
	} else if decoded.UserAgent != "test" || decoded.BlockchainHeight != 5 || decoded.BlockchainVersion != 6 || !decoded.Requests.IsEmpty() {
		t.Fatalf("decoded announcement mismatch: %+v", decoded)
	}

	// another sender, another fixed public key
	if _, err := LocalDiscoveryVerify(payload, otherPublic, fixedPublic); err == nil {
		t.Fatal("signature accepted for another sender")
	}
	if _, err := LocalDiscoveryVerify(payload, senderPublic, fixedOtherPublic); err == nil {
		t.Fatal("signature accepted for another fixed public key")
	}

	// signature of another peer
	if _, err := LocalDiscoveryVerify(LocalDiscoverySign(announcement, otherPrivate, fixedPublic), senderPublic, fixedPublic); err == nil {
		t.Fatal("signature of another peer accepted")
	}

	// missing signature: the announcement alone, and every size below the signature size
	if _, err := LocalDiscoveryVerify(announcement, senderPublic, fixedPublic); err == nil {
		t.Fatal("announcement without signature accepted")
	}
	for n := 0; n < 64; n++ {
		if _, err := LocalDiscoveryVerify(payload[:n], senderPublic, fixedPublic); err == nil {
			t.Fatalf("payload of %d bytes accepted", n)
		}
	}

	// every single bit flip
	for n := 0; n < len(payload)*8; n++ {
		tampered := append([]byte{}, payload...)
		tampered[n/8] ^= 1 << (n % 8)

		if _, err := LocalDiscoveryVerify(tampered, senderPublic, fixedPublic); err == nil {
			t.Fatalf("flipped bit %d accepted", n)
		}
	}

	// invalid sender public key
	if _, err := LocalDiscoveryVerify(payload, nil, fixedPublic); err == nil {
		t.Fatal("empty sender public key accepted")
	}
}
