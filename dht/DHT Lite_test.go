/*
File Name:  DHT Lite_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package dht

import (
	"bytes"
	"testing"
)

// TestRefreshBuckets checks which lookups a refresh of the routing table starts: one for the own ID, then one for every bucket that holds fewer nodes than the bucket size,
// from the bucket of the farthest nodes to the bucket of the closest node in the routing table.
func TestRefreshBuckets(t *testing.T) {
	for _, count := range []int{0, 1, 3, 20, 200} {
		self := testID(t)
		dht := NewDHT(&Node{ID: self}, 256, 20, 5)
		dht.ShouldEvict = func(node1, node2 *Node) bool { return false }

		for n := 0; n < count; n++ {
			dht.AddNode(&Node{ID: testID(t)})
		}

		// Every node answers that it knows no node close to the key, which ends the lookup. The key of every lookup is recorded.
		var keys [][]byte

		dht.SendRequest = func(request *InformationRequest) {
			if len(keys) == 0 || !bytes.Equal(keys[len(keys)-1], request.Key) {
				keys = append(keys, request.Key)
			}

			for _, node := range request.Nodes {
				request.QueueResult(&NodeMessage{SenderID: node.ID})
				request.Done()
			}
		}

		// The buckets expected to be refreshed, in order.
		total := dht.ht.getTotalNodesPerBucket()
		closest := -1
		for bucket := len(total) - 1; bucket >= 0; bucket-- {
			if total[bucket] > 0 {
				closest = bucket
			}
		}

		var expected []int
		for bucket := len(total) - 1; closest >= 0 && bucket >= closest; bucket-- {
			if total[bucket] < 20 {
				expected = append(expected, bucket)
			}
		}

		if err := dht.RefreshBuckets(); err != nil {
			t.Fatalf("%d nodes: %v", count, err)
		}

		// Without a node in the routing table no request is sent.
		if count == 0 {
			if len(keys) != 0 {
				t.Fatalf("%d nodes: %d lookups sent a request", count, len(keys))
			}
			continue
		}

		if len(keys) != 1+len(expected) {
			t.Fatalf("%d nodes: %d lookups, expected the own ID and %d buckets", count, len(keys), len(expected))
		} else if !bytes.Equal(keys[0], self) {
			t.Fatalf("%d nodes: first lookup is for %x, expected the own ID", count, keys[0])
		}

		for n, bucket := range expected {
			if index := dht.ht.getBucketIndexFromDifferingBit(keys[n+1]); index != bucket {
				t.Fatalf("%d nodes: lookup %d is for an ID in bucket %d, expected bucket %d", count, n+1, index, bucket)
			}
		}

		t.Logf("%d nodes: closest node in bucket %d, lookups for the own ID and %d buckets", count, closest, len(expected))
	}
}
