/*
File Name:  Hash Table_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package dht

import (
	"bytes"
	"sort"
	"testing"
)

// testXorCompare compares the XOR distances of two IDs to the target. It returns -1 if id1 is closer, 1 if id2 is closer, 0 if both are equal.
func testXorCompare(id1, id2, target []byte) int {
	for n := range target {
		distance1, distance2 := id1[n]^target[n], id2[n]^target[n]
		if distance1 < distance2 {
			return -1
		} else if distance1 > distance2 {
			return 1
		}
	}
	return 0
}

// testClosest returns the count nodes closest to the target out of all nodes, closest first. accept decides which nodes are considered.
func testClosest(nodes []*Node, target []byte, count int, accept func(node *Node) bool) (closest []*Node) {
	for _, node := range nodes {
		if accept(node) {
			closest = append(closest, node)
		}
	}

	sort.Slice(closest, func(i, j int) bool {
		return testXorCompare(closest[i].ID, closest[j].ID, target) < 0
	})

	if len(closest) > count {
		closest = closest[:count]
	}
	return closest
}

// testCompareNodes fails the test if the two lists do not hold the same nodes in the same order.
func testCompareNodes(t *testing.T, name string, result, expected []*Node) {
	t.Helper()

	if len(result) != len(expected) {
		t.Fatalf("%s: %d nodes returned, expected %d", name, len(result), len(expected))
	}
	for n := range expected {
		if !bytes.Equal(result[n].ID, expected[n].ID) {
			t.Fatalf("%s: node %d is %x, expected %x", name, n, result[n].ID, expected[n].ID)
		}
	}
}

// testIDInBucket returns an ID that falls into the given bucket of a routing table whose own ID is all zero bits. The last byte is set to tail.
func testIDInBucket(bucket int, tail byte) []byte {
	id := make([]byte, 32)
	bit := 255 - bucket
	id[bit/8] = 1 << (7 - bit%8)
	id[31] |= tail
	return id
}

// TestGetClosestContacts checks that the nodes returned are the closest of the entire routing table by XOR distance to the target, closest first.
func TestGetClosestContacts(t *testing.T) {
	dht := NewDHT(&Node{ID: testID(t)}, 256, 20, 5, 5, 20)

	for n := 0; n < 2000; n++ {
		dht.AddNode(&Node{ID: testID(t)})
	}

	nodes := dht.Nodes()
	if len(nodes) < 100 {
		t.Fatalf("routing table holds %d nodes only", len(nodes))
	}

	all := func(node *Node) bool { return true }
	half := func(node *Node) bool { return node.ID[31]&1 == 0 }

	for n := 0; n < 500; n++ {
		target := testID(t)

		// The count is below the bucket size, above it, and above the size of the routing table.
		for _, count := range []int{5, 30, len(nodes) + 1} {
			testCompareNodes(t, "no filter", dht.GetClosestContacts(count, target, nil), testClosest(nodes, target, count, all))
			testCompareNodes(t, "filter", dht.GetClosestContacts(count, target, half), testClosest(nodes, target, count, half))
		}

		// Ignore the closest and the third closest node.
		closest := testClosest(nodes, target, 3, all)
		ignored1, ignored2 := closest[0].ID, closest[2].ID
		notIgnored := func(node *Node) bool {
			return !bytes.Equal(node.ID, ignored1) && !bytes.Equal(node.ID, ignored2)
		}

		testCompareNodes(t, "ignored nodes", dht.GetClosestContacts(5, target, nil, ignored1, ignored2), testClosest(nodes, target, 5, notIgnored))
	}

	// The own ID as target.
	testCompareNodes(t, "own ID", dht.GetClosestContacts(5, dht.GetSelfID(), nil), testClosest(nodes, dht.GetSelfID(), 5, all))

	if result := dht.GetClosestContacts(0, testID(t), nil); len(result) != 0 {
		t.Fatalf("count 0: %d nodes returned", len(result))
	}
}

// TestGetClosestContactsBucketOrder checks where the nodes come from when the bucket of the target holds fewer nodes than requested:
// first the bucket of the target, then the buckets of a lower index, then the buckets of a higher index.
func TestGetClosestContactsBucketOrder(t *testing.T) {
	dht := NewDHT(&Node{ID: make([]byte, 32)}, 256, 20, 5, 5, 20)

	target := testIDInBucket(100, 0)

	// Listed by distance to the target, closest first.
	expected := []*Node{
		{ID: testIDInBucket(100, 1)}, // bucket of the target
		{ID: testIDInBucket(100, 2)},
		{ID: testIDInBucket(40, 0)}, // lower index
		{ID: testIDInBucket(99, 0)},
		{ID: testIDInBucket(101, 0)}, // higher index
		{ID: testIDInBucket(101, 1)},
		{ID: testIDInBucket(200, 0)},
	}

	// The nodes are added in reverse order.
	for n := len(expected) - 1; n >= 0; n-- {
		dht.AddNode(expected[n])
	}

	for count := 1; count <= len(expected)+1; count++ {
		want := expected
		if count < len(expected) {
			want = expected[:count]
		}
		testCompareNodes(t, "bucket order", dht.GetClosestContacts(count, target, nil), want)
	}
}

// TestGetRandomIDFromBucket checks the IDs generated for the refresh of a bucket: the size of the ID, and that the ID falls into the bucket.
func TestGetRandomIDFromBucket(t *testing.T) {
	selfZero := make([]byte, 32)
	selfOne := bytes.Repeat([]byte{0xFF}, 32)

	for _, self := range [][]byte{testID(t), selfZero, selfOne} {
		dht := NewDHT(&Node{ID: self}, 256, 20, 5, 5, 20)

		for bucket := 0; bucket < 256; bucket++ {
			for n := 0; n < 100; n++ {
				id := dht.ht.getRandomIDFromBucket(bucket)

				if len(id) != 32 {
					t.Fatalf("own ID %x: ID generated for bucket %d has %d bytes", self, bucket, len(id))
				} else if index := dht.ht.getBucketIndexFromDifferingBit(id); index != bucket {
					t.Fatalf("own ID %x: ID %x generated for bucket %d falls into bucket %d", self, id, bucket, index)
				}

				// A lookup accepts the ID as key.
				if _, err := dht.FindNode(id); err != nil {
					t.Fatalf("own ID %x: lookup for ID %x generated for bucket %d: %v", self, id, bucket, err)
				}
			}
		}
	}
}

// TestInsertNodeFullBucket checks a bucket that holds the bucket size of nodes: it takes no new node and keeps its order.
// After one of its nodes is removed, the next node for the bucket is taken.
func TestInsertNodeFullBucket(t *testing.T) {
	dht := NewDHT(&Node{ID: make([]byte, 32)}, 256, 20, 5, 5, 20)

	const bucket = 200
	var nodes []*Node

	for n := 0; n < 20; n++ {
		nodes = append(nodes, &Node{ID: testIDInBucket(bucket, byte(n))})
		dht.AddNode(nodes[n])
	}

	// Another bucket is filled with a single node.
	other := &Node{ID: testIDInBucket(bucket+1, 0)}
	dht.AddNode(other)

	node21 := &Node{ID: testIDInBucket(bucket, 20)}
	dht.AddNode(node21)

	if dht.IsNodeContact(node21.ID) != nil {
		t.Fatal("node 21 was added to a full bucket")
	}
	testCompareNodes(t, "full bucket", dht.ht.RoutingTable[bucket], nodes)

	if dht.NumNodes() != 21 {
		t.Fatalf("%d nodes in the routing table, expected 21", dht.NumNodes())
	}

	// Remove one node. The bucket takes the next node at its end.
	dht.RemoveNode(nodes[7].ID)
	dht.AddNode(node21)

	expected := append(append(append([]*Node{}, nodes[:7]...), nodes[8:]...), node21)
	testCompareNodes(t, "after removal", dht.ht.RoutingTable[bucket], expected)

	// A node that is in the bucket already moves to its end.
	dht.AddNode(nodes[0])

	expected = append(expected[1:], nodes[0])
	testCompareNodes(t, "added again", dht.ht.RoutingTable[bucket], expected)

	if dht.NumNodes() != 21 {
		t.Fatalf("%d nodes in the routing table, expected 21", dht.NumNodes())
	}
}
