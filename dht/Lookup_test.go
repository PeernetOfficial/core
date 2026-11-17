/*
File Name:  Lookup_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package dht

import (
	"bytes"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// testNetwork is a simulated network. Every node has its own routing table and answers a request with the closest nodes of that table.
type testNetwork struct {
	ids   [][]byte
	nodes map[string]*DHT
}

// newTestNetwork creates a network of count nodes. Every node is added to the routing table of every other node.
func newTestNetwork(t *testing.T, count int) (network *testNetwork) {
	network = &testNetwork{nodes: make(map[string]*DHT)}

	for n := 0; n < count; n++ {
		id := testID(t)
		node := NewDHT(&Node{ID: id}, 256, 20, 5)
		node.ShouldEvict = func(node1, node2 *Node) bool { return false }

		network.ids = append(network.ids, id)
		network.nodes[string(id)] = node
	}

	for _, node := range network.nodes {
		for _, id := range network.ids {
			if !bytes.Equal(id, node.GetSelfID()) {
				node.AddNode(&Node{ID: id})
			}
		}
	}

	return network
}

var testNetworkLarge *testNetwork
var testNetworkLargeOnce sync.Once

// testNetwork2000 returns a network of 2000 nodes that is shared by the tests.
func testNetwork2000(t *testing.T) *testNetwork {
	testNetworkLargeOnce.Do(func() {
		testNetworkLarge = newTestNetwork(t, 2000)
	})
	return testNetworkLarge
}

// testLookup is a single search started by one node of the simulated network.
type testLookup struct {
	search   *SearchClient
	requests int     // Count of nodes that were sent the request.
	reported []*Node // All nodes reported in responses.
	failed   string  // Set if a request was sent to a node that was not the closest one not yet contacted.
}

// start runs a search from the node with the given ID. Every node that is sent the request answers with the 5 closest nodes of its routing table, the requesting node excluded.
// It answers at once, or if delayed is set after 1 to 20 ms.
// Each information request is checked: its nodes must be closer to the key than every node of the search list that is not yet contacted.
func (network *testNetwork) start(self []byte, action int, key []byte, alpha int, delayed bool) (lookup *testLookup) {
	lookup = &testLookup{}
	dht := network.nodes[string(self)]

	dht.SendRequest = func(request *InformationRequest) {
		list := lookup.search.list

		uncontacted := 0
		for _, candidate := range list.Nodes {
			if list.Contacted[string(candidate.ID)] {
				continue
			}
			uncontacted++

			for _, node := range request.Nodes {
				if lookup.failed == "" && testXorCompare(node.ID, candidate.ID, key) > 0 {
					lookup.failed = "request sent to a node while a closer node was not yet contacted"
				}
			}
		}

		if len(request.Nodes) < alpha && uncontacted > 0 && lookup.failed == "" {
			lookup.failed = "request sent to fewer nodes than alpha while nodes were not yet contacted"
		}

		for _, node := range request.Nodes {
			lookup.requests++

			closest := network.nodes[string(node.ID)].GetClosestContacts(5, key, nil, self)
			lookup.reported = append(lookup.reported, closest...)

			message := &NodeMessage{SenderID: node.ID, Closest: closest}

			if delayed {
				go func() {
					time.Sleep(time.Duration(1+rand.Intn(20)) * time.Millisecond)
					request.QueueResult(message)
					request.Done()
				}()
			} else {
				request.QueueResult(message)
				request.Done()
			}
		}
	}

	lookup.search = dht.NewSearch(action, key, dht.TimeoutSearch, dht.TimeoutIR, alpha)
	lookup.search.SearchAway()

	return lookup
}

// checkEnd checks a search that ended without result: all reported nodes are in the search list, and it holds no node closer to the key than the closest node contacted.
func (lookup *testLookup) checkEnd(t *testing.T, key []byte) {
	t.Helper()
	list := lookup.search.list

	if lookup.failed != "" {
		t.Fatal(lookup.failed)
	}

	candidates := make(map[string]struct{})
	for _, node := range list.Nodes {
		candidates[string(node.ID)] = struct{}{}
	}
	for _, node := range lookup.reported {
		if _, ok := candidates[string(node.ID)]; !ok {
			t.Fatalf("reported node %x is not in the search list", node.ID)
		}
	}

	closest := testClosest(list.Nodes, key, 1, func(node *Node) bool { return true })
	if !list.Contacted[string(closest[0].ID)] {
		t.Fatalf("search ended while the closest node %x in the search list was not contacted", closest[0].ID)
	}
}

// TestLookupFound searches for nodes that exist in a simulated network of 2000 nodes.
// Every request must go to the closest nodes not yet contacted, and the node must be found.
func TestLookupFound(t *testing.T) {
	network := testNetwork2000(t)
	requests := 0

	const lookups = 500

	for n := 0; n < lookups; n++ {
		self, key := network.ids[n], network.ids[len(network.ids)-1-n]

		lookup := network.start(self, ActionFindNode, key, 5, false)

		result, ok := <-lookup.search.Results
		if !ok {
			t.Fatalf("lookup %d: node %x not found after %d requests", n, key, lookup.requests)
		} else if !bytes.Equal(result.TargetNode.ID, key) {
			t.Fatalf("lookup %d: result is node %x, searched for %x", n, result.TargetNode.ID, key)
		}

		if lookup.failed != "" {
			t.Fatalf("lookup %d: %s", n, lookup.failed)
		}

		requests += lookup.requests
	}

	t.Logf("%d lookups for existing nodes, %.1f requests per lookup", lookups, float64(requests)/lookups)
}

// TestLookupNotFound searches for IDs that no node has in a simulated network of 2000 nodes, and for the own ID.
// The search must end when no response brings a node closer to the key than the closest node contacted, long before the timeout of the search.
func TestLookupNotFound(t *testing.T) {
	network := testNetwork2000(t)
	requests := 0

	const lookups = 500

	for n := 0; n < lookups; n++ {
		self, key := network.ids[n], testID(t)
		if n%10 == 0 {
			key = self
		}

		timeStart := time.Now()
		lookup := network.start(self, ActionFindNode, key, 5, false)

		if result, ok := <-lookup.search.Results; ok {
			t.Fatalf("lookup %d: unexpected result from %x", n, result.SenderID)
		}

		if elapsed := time.Since(timeStart); elapsed > network.nodes[string(self)].TimeoutIR/2 {
			t.Fatalf("lookup %d: search ended after %s", n, elapsed)
		}

		lookup.checkEnd(t, key)

		if max := 5 * (MaxLevel + 1); lookup.requests > max {
			t.Fatalf("lookup %d: %d requests sent, expected at most %d", n, lookup.requests, max)
		}

		requests += lookup.requests
	}

	t.Logf("%d lookups for IDs that no node has, %.1f requests per lookup", lookups, float64(requests)/lookups)
}

// TestSearchValueOrder runs the searches for a value in a simulated network of 100 nodes. No node stores the value.
// Every request must go to the closest nodes not yet contacted. The search ends when every node of the search list was contacted.
func TestSearchValueOrder(t *testing.T) {
	network := newTestNetwork(t, 100)

	for n := 0; n < 50; n++ {
		for _, action := range []int{ActionFindValue, ActionFindValueClosest} {
			self, key := network.ids[n], testID(t)

			lookup := network.start(self, action, key, 5, false)

			if result, ok := <-lookup.search.Results; ok {
				t.Fatalf("search %d: unexpected result from %x", n, result.SenderID)
			}

			lookup.checkEnd(t, key)

			for _, node := range lookup.search.list.Nodes {
				if !lookup.search.list.Contacted[string(node.ID)] {
					t.Fatalf("search %d: search ended while node %x in the search list was not contacted", n, node.ID)
				}
			}
		}
	}
}

// TestLookupDelayed searches in a simulated network of 2000 nodes in which every node answers after 1 to 20 ms: for nodes that exist, and for IDs that no node has.
func TestLookupDelayed(t *testing.T) {
	network := testNetwork2000(t)
	requestsFound, requestsNotFound := 0, 0

	const lookups = 100

	for n := 0; n < lookups; n++ {
		self, key := network.ids[n], network.ids[len(network.ids)-1-n]

		lookup := network.start(self, ActionFindNode, key, 5, true)

		result, ok := <-lookup.search.Results
		if !ok {
			t.Fatalf("lookup %d: node %x not found after %d requests", n, key, lookup.requests)
		} else if !bytes.Equal(result.TargetNode.ID, key) {
			t.Fatalf("lookup %d: result is node %x, searched for %x", n, result.TargetNode.ID, key)
		}

		<-lookup.search.TerminateSignal
		if lookup.failed != "" {
			t.Fatalf("lookup %d: %s", n, lookup.failed)
		}

		requestsFound += lookup.requests
	}

	for n := 0; n < lookups; n++ {
		self, key := network.ids[n], testID(t)

		lookup := network.start(self, ActionFindNode, key, 5, true)

		if result, ok := <-lookup.search.Results; ok {
			t.Fatalf("lookup %d: unexpected result from %x", n, result.SenderID)
		}

		lookup.checkEnd(t, key)

		requestsNotFound += lookup.requests
	}

	t.Logf("%d lookups each: %.1f requests per lookup for existing nodes, %.1f for IDs that no node has", lookups, float64(requestsFound)/lookups, float64(requestsNotFound)/lookups)
}
