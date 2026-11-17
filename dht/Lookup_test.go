/*
File Name:  Lookup_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package dht

import (
	"bytes"
	"encoding/binary"
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
		node := NewDHT(&Node{ID: id}, 256, 20, 5, 5)

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
	delayed  bool    // Set if the nodes answer after 1 to 20 ms.
	requests int     // Count of nodes that were sent the request.
	reported []*Node // All nodes reported in responses.
	failed   string  // Set if a request was sent to a node that was not the closest one not yet contacted.
}

// start runs a search from the node with the given ID. Every node that is sent the request answers with the 5 closest nodes of its routing table, the requesting node excluded.
// It answers at once, or if delayed is set after 1 to 20 ms.
// Each information request is checked: its nodes must be closer to the key than every node of the search list that is not yet contacted.
func (network *testNetwork) start(self []byte, action int, key []byte, alpha int, delayed bool) (lookup *testLookup) {
	lookup = &testLookup{delayed: delayed}
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

// checkEnd checks a search that ended without result.
// A search for a node: the beta closest nodes of the search list have all answered, the nodes that did not answer left out.
// A search for a value: it holds no node closer to the key than the closest node contacted.
// All reported nodes are in the search list. A search for a node ends while requests are under way, the check is left out if the nodes answer delayed.
func (lookup *testLookup) checkEnd(t *testing.T, key []byte) {
	t.Helper()
	search := lookup.search
	list := search.list

	if lookup.failed != "" {
		t.Fatal(lookup.failed)
	}

	if search.Action != ActionFindNode || !lookup.delayed {
		candidates := make(map[string]struct{})
		for _, node := range list.Nodes {
			candidates[string(node.ID)] = struct{}{}
		}
		for _, node := range lookup.reported {
			if _, ok := candidates[string(node.ID)]; !ok {
				t.Fatalf("reported node %x is not in the search list", node.ID)
			}
		}
	}

	if search.Action == ActionFindNode {
		closest := testClosest(list.Nodes, key, search.beta, func(node *Node) bool { return !search.unanswered[string(node.ID)] })
		for _, node := range closest {
			if !search.answered[string(node.ID)] {
				t.Fatalf("search ended while node %x, one of the %d closest nodes in the search list, did not answer", node.ID, search.beta)
			}
		}
		return
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
// The search must end when the beta closest nodes of the search list have all answered, long before the timeout of the search.
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

// testIDDistance returns the ID at the given XOR distance to the key.
func testIDDistance(key []byte, distance uint32) (id []byte) {
	id = append([]byte{}, key...)
	binary.BigEndian.PutUint32(id[len(id)-4:], binary.BigEndian.Uint32(id[len(id)-4:])^distance)
	return id
}

// testLookupNodes runs a search for the node with the ID key. The routing table of the searching node holds the nodes of table.
// A node that is sent the request answers at once with the nodes that answer returns for it, or does not answer if ok is false.
// It returns the node found, the count of requests each node was sent and the time the search took.
func testLookupNodes(t *testing.T, key []byte, table []*Node, timeoutIR time.Duration, answer func(node *Node) (closest []*Node, ok bool)) (search *SearchClient, found *Node, requests map[string]int, elapsed time.Duration) {
	dht := NewDHT(&Node{ID: testID(t)}, 256, 20, 5, 5)
	for _, node := range table {
		dht.AddNode(node)
	}

	requests = make(map[string]int)

	dht.SendRequest = func(request *InformationRequest) {
		for _, node := range request.Nodes {
			requests[string(node.ID)]++

			if closest, ok := answer(node); ok {
				request.QueueResult(&NodeMessage{SenderID: node.ID, Closest: closest})
				request.Done()
			}
		}
	}

	timeStart := time.Now()

	search = dht.NewSearch(ActionFindNode, key, 2*time.Second, timeoutIR, 5)
	search.SearchAway()

	if result, ok := <-search.Results; ok {
		found = result.TargetNode
	}
	<-search.TerminateSignal

	return search, found, requests, time.Since(timeStart)
}

// TestLookupClosestNode searches for a node that only node N knows. N is reported by node A and is not the closest node to the key:
// the closest node S does not answer, answers without any node, or answers with nodes it knows. The node must be found in each case.
func TestLookupClosestNode(t *testing.T) {
	for _, test := range []string{"does not answer", "answers without any node", "answers with nodes it knows"} {
		key := testID(t)
		target := &Node{ID: key}
		nodeS := &Node{ID: testIDDistance(key, 1)}
		nodeN := &Node{ID: testIDDistance(key, 2)}
		nodeA := &Node{ID: testIDDistance(key, 8)}
		nodeB := &Node{ID: testIDDistance(key, 9)}
		nodeC := &Node{ID: testIDDistance(key, 10)}
		nodeD := &Node{ID: testIDDistance(key, 11)}

		_, found, requests, _ := testLookupNodes(t, key, []*Node{nodeS, nodeA, nodeB, nodeC, nodeD}, 100*time.Millisecond, func(node *Node) (closest []*Node, ok bool) {
			switch string(node.ID) {
			case string(nodeS.ID):
				switch test {
				case "does not answer":
					return nil, false
				case "answers without any node":
					return nil, true
				}
				return []*Node{nodeA, nodeB}, true
			case string(nodeN.ID):
				return []*Node{target, nodeS}, true
			case string(nodeA.ID):
				return []*Node{nodeN, nodeB}, true
			}
			return []*Node{nodeA}, true
		})

		if found == nil {
			t.Errorf("closest node %s: node not found, node N was sent %d requests", test, requests[string(nodeN.ID)])
		} else if !bytes.Equal(found.ID, key) {
			t.Errorf("closest node %s: result is node %x, searched for %x", test, found.ID, key)
		}
	}
}

// TestLookupNotAnswered searches for an ID that no node has, with nodes that do not answer.
func TestLookupNotAnswered(t *testing.T) {
	key := testID(t)
	nodeS := &Node{ID: testIDDistance(key, 1)}
	nodeA := &Node{ID: testIDDistance(key, 8)}
	nodeB := &Node{ID: testIDDistance(key, 9)}
	nodeC := &Node{ID: testIDDistance(key, 10)}
	nodeD := &Node{ID: testIDDistance(key, 11)}
	nodeF := &Node{ID: testIDDistance(key, 16)}
	nodeG := &Node{ID: testIDDistance(key, 17)}

	// The closest node S does not answer. The next closest node of the routing table, F, takes its place among the beta closest nodes and is sent the request.
	search, found, requests, elapsed := testLookupNodes(t, key, []*Node{nodeS, nodeA, nodeB, nodeC, nodeD, nodeF, nodeG}, 100*time.Millisecond, func(node *Node) (closest []*Node, ok bool) {
		return []*Node{nodeA, nodeB}, !bytes.Equal(node.ID, nodeS.ID)
	})

	if found != nil {
		t.Fatalf("closest node does not answer: unexpected result %x", found.ID)
	}
	for name, node := range map[string]*Node{"S": nodeS, "A": nodeA, "B": nodeB, "C": nodeC, "D": nodeD, "F": nodeF} {
		if count := requests[string(node.ID)]; count != 1 {
			t.Errorf("closest node does not answer: node %s was sent %d requests, expected 1", name, count)
		}
	}
	if !search.unanswered[string(nodeS.ID)] || len(search.unanswered) != 1 {
		t.Errorf("closest node does not answer: %d nodes are recorded as not answering, expected node S only", len(search.unanswered))
	}
	for name, node := range map[string]*Node{"A": nodeA, "B": nodeB, "C": nodeC, "D": nodeD, "F": nodeF} {
		if !search.answered[string(node.ID)] {
			t.Errorf("closest node does not answer: node %s is not recorded as answered", name)
		}
	}
	if elapsed > time.Second {
		t.Errorf("closest node does not answer: search ended after %s", elapsed)
	}

	// No node answers. Every node of the routing table is sent the request, then no node is left to ask.
	_, found, requests, elapsed = testLookupNodes(t, key, []*Node{nodeS, nodeA, nodeB, nodeC, nodeD, nodeF, nodeG}, 100*time.Millisecond, func(node *Node) (closest []*Node, ok bool) {
		return nil, false
	})

	if found != nil {
		t.Fatalf("no node answers: unexpected result %x", found.ID)
	}
	if len(requests) != 7 {
		t.Errorf("no node answers: %d nodes were sent the request, expected 7", len(requests))
	}
	if elapsed > time.Second {
		t.Errorf("no node answers: search ended after %s", elapsed)
	}

	// Node G does not answer and is not one of the beta closest nodes once A reports S and F. The search ends while the request to G is under way.
	_, found, requests, elapsed = testLookupNodes(t, key, []*Node{nodeA, nodeB, nodeC, nodeD, nodeG}, time.Second, func(node *Node) (closest []*Node, ok bool) {
		return []*Node{nodeS, nodeF}, !bytes.Equal(node.ID, nodeG.ID)
	})

	if found != nil {
		t.Fatalf("far node does not answer: unexpected result %x", found.ID)
	}
	if len(requests) != 7 {
		t.Errorf("far node does not answer: %d nodes were sent the request, expected 7", len(requests))
	}
	if elapsed > time.Second/2 {
		t.Errorf("far node does not answer: search ended after %s", elapsed)
	}
}

// TestLookupMaxLevel searches for an ID that no node has, while every node answers with 5 nodes that are closer to the key than all nodes before.
// The search ends at the max level.
func TestLookupMaxLevel(t *testing.T) {
	key := testID(t)
	distance := uint32(1 << 30)

	next := func() (nodes []*Node) {
		for n := 0; n < 5; n++ {
			distance--
			nodes = append(nodes, &Node{ID: testIDDistance(key, distance)})
		}
		return nodes
	}

	_, found, requests, elapsed := testLookupNodes(t, key, next(), 100*time.Millisecond, func(node *Node) (closest []*Node, ok bool) {
		return next(), true
	})

	if found != nil {
		t.Fatalf("unexpected result %x", found.ID)
	}
	if expected := 5 * (MaxLevel + 1); len(requests) != expected {
		t.Errorf("%d nodes were sent the request, expected %d", len(requests), expected)
	}
	if elapsed > time.Second {
		t.Errorf("search ended after %s", elapsed)
	}
}
