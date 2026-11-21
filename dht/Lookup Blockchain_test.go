/*
File Name:  Lookup Blockchain_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package dht

import (
	"bytes"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// testCollectStoring reads the storing nodes that a search hands over until the search is terminated.
func testCollectStoring(search *SearchClient) (storing []*SearchResult) {
	for {
		select {
		case result := <-search.Storing:
			storing = append(storing, result)
		case <-search.TerminateSignal:
			return storing
		}
	}
}

// TestLookupBlockchain searches for the nodes that store a blockchain in a simulated network of 2000 nodes, with nodes that answer at once and nodes that answer delayed, one at a time in random order.
// One of the gamma closest nodes to the key reports two storing nodes: the closest one, the 6th, the 13th or the 20th. In every 8th search the key is the ID of a node, which reports itself as storing node.
// The search must end when the gamma closest nodes have all answered, long before the timeout of the search, and hand over every storing node.
func TestLookupBlockchain(t *testing.T) {
	network := testNetwork2000(t)
	defer func() { network.storing = nil }()
	random := testRandom(2)

	for _, delayed := range []bool{false, true} {
		lookups, requests, activeMax := 200, 0, 0
		newID := func() []byte { return testID(t) }
		if delayed {
			lookups = 80
			newID = func() []byte { return testIDRandom(random) }
		}

		run := func(t *testing.T) {
			for n := 0; n < lookups; n++ {
				self, key := network.ids[n], newID()
				keyIsNode := n%8 == 0
				if keyIsNode {
					key = network.ids[len(network.ids)-1-n]
				}

				// The gamma closest nodes of the network to the key. Every node knows all nodes, the searching node is not reported.
				closest := testClosest(network.all, key, 20, func(node *Node) bool { return !bytes.Equal(node.ID, self) })

				keeper := closest[[]int{5, 12, 19, 0}[n%4]]
				storingNodes := []*Node{{ID: newID()}, {ID: newID()}}
				network.storing = map[string][]*Node{string(keeper.ID): storingNodes}

				expected := map[string]string{string(storingNodes[0].ID): string(keeper.ID), string(storingNodes[1].ID): string(keeper.ID)}
				if keyIsNode {
					network.storing[string(key)] = []*Node{{ID: key}}
					expected[string(key)] = string(key)
				}

				timeStart := time.Now()
				lookup := network.start(self, ActionFindBlockchain, key, 5, delayed)

				var storing []*SearchResult
				if delayed {
					storing = lookup.run(t, random)
				} else {
					storing = testCollectStoring(lookup.search)
				}

				if result, ok := <-lookup.search.Results; ok {
					t.Fatalf("lookup %d: unexpected result from %x", n, result.SenderID)
				}
				if elapsed := time.Since(timeStart); elapsed > network.nodes[string(self)].TimeoutIR/2 {
					t.Fatalf("lookup %d: search ended after %s", n, elapsed)
				}

				lookup.checkEnd(t, key)

				for rank, node := range closest {
					if !lookup.search.answered[string(node.ID)] {
						t.Fatalf("lookup %d: search ended after %d requests while node %x, number %d of the closest nodes to the key, did not answer", n, lookup.requests, node.ID, rank+1)
					}
				}

				if len(storing) != len(expected) {
					t.Fatalf("lookup %d: %d storing nodes handed over, expected %d", n, len(storing), len(expected))
				}
				for _, result := range storing {
					sender, ok := expected[string(result.StoringNode.ID)]
					if !ok || sender != string(result.SenderID) || !bytes.Equal(result.Key, key) || result.Action != ActionFindBlockchain {
						t.Fatalf("lookup %d: unexpected storing node %x reported by %x", n, result.StoringNode.ID, result.SenderID)
					}
					delete(expected, string(result.StoringNode.ID))
				}

				if max := MaxRequests; lookup.requests > max {
					t.Fatalf("lookup %d: %d requests sent, expected at most %d", n, lookup.requests, max)
				}

				requests += lookup.requests
				if lookup.activeMax > activeMax {
					activeMax = lookup.activeMax
				}
			}
		}

		// The nodes answer delayed inside a bubble, see run.
		if delayed {
			synctest.Test(t, run)
		} else {
			run(t)
		}

		if delayed {
			t.Logf("%d lookups with delayed answers, %.1f requests per lookup, at most %d requests under way at once", lookups, float64(requests)/float64(lookups), activeMax)
		} else {
			t.Logf("%d lookups, %.1f requests per lookup", lookups, float64(requests)/float64(lookups))
		}
	}
}

// testLookupBlockchainNodes runs a search for the nodes that store the blockchain of key. The routing table of the searching node holds the nodes of table.
// A node that is sent the request answers after delay with the closest and storing nodes that answer returns for it, or does not answer if ok is false.
// read is called for each storing node that is handed over. It returns the search, the count of requests each node was sent and the time the search took.
func testLookupBlockchainNodes(t *testing.T, key []byte, table []*Node, timeoutIR time.Duration, answer func(node *Node) (closest, storing []*Node, ok bool, delay time.Duration), read func(search *SearchClient, result *SearchResult)) (search *SearchClient, requests map[string]int, elapsed time.Duration) {
	dht := NewDHT(&Node{ID: testID(t)}, 256, 20, 5, 5, 20)
	for _, node := range table {
		dht.AddNode(node)
	}

	requests = make(map[string]int)

	dht.SendRequest = func(request *InformationRequest) {
		for _, node := range request.Nodes {
			requests[string(node.ID)]++

			closest, storing, ok, delay := answer(node)
			if !ok {
				continue
			}

			message := &NodeMessage{SenderID: node.ID, Closest: closest, Storing: storing}

			if delay > 0 {
				go func() {
					time.Sleep(delay)
					request.QueueResult(message)
					request.Done()
				}()
			} else {
				request.QueueResult(message)
				request.Done()
			}
		}
	}

	timeStart := time.Now()

	search = dht.NewSearch(ActionFindBlockchain, key, 2*time.Second, timeoutIR, 5)
	search.SearchAway()

	for _, result := range testCollectStoring(search) {
		read(search, result)
	}

	return search, requests, time.Since(timeStart)
}

// TestLookupBlockchainChain searches for the nodes that store a blockchain while each node reports one node that is closer to the key, 30 nodes in a row.
// Each answer starts a request to one node. The search ends when all 30 nodes have answered: the count of information requests does not end it.
func TestLookupBlockchainChain(t *testing.T) {
	key := testID(t)

	// Nodes 1 to 30 by their distance to the key. The routing table holds node 30. Node n reports node n-1, node 1 reports no node.
	var nodes []*Node
	for distance := uint32(1); distance <= 30; distance++ {
		nodes = append(nodes, &Node{ID: testIDDistance(key, distance)})
	}

	search, requests, _ := testLookupBlockchainNodes(t, key, nodes[29:], 200*time.Millisecond, func(node *Node) (closest, storing []*Node, ok bool, delay time.Duration) {
		for number := 1; number < len(nodes); number++ {
			if bytes.Equal(node.ID, nodes[number].ID) {
				return nodes[number-1 : number], nil, true, 0
			}
		}
		return nil, nil, true, 0
	}, func(search *SearchClient, result *SearchResult) {
		t.Fatalf("unexpected storing node %x", result.StoringNode.ID)
	})

	for number, node := range nodes {
		if requests[string(node.ID)] != 1 || !search.answered[string(node.ID)] {
			t.Fatalf("node %d was sent %d requests, answered %t; %d nodes were sent the request, expected all 30", number+1, requests[string(node.ID)], search.answered[string(node.ID)], len(requests))
		}
	}
	if len(requests) != len(nodes) {
		t.Fatalf("%d nodes were sent the request, expected %d", len(requests), len(nodes))
	}
}

// TestLookupBlockchainNotAnswered searches for the nodes that store a blockchain with nodes that do not answer.
func TestLookupBlockchainNotAnswered(t *testing.T) {
	key := testID(t)

	// Nodes 1 to 23 by their distance to the key. The routing table holds nodes 1 to 20.
	var nodes []*Node
	for distance := uint32(1); distance <= 23; distance++ {
		nodes = append(nodes, &Node{ID: testIDDistance(key, distance)})
	}
	isNode := func(node *Node, number int) bool { return bytes.Equal(node.ID, nodes[number-1].ID) }

	// The 3 closest nodes do not answer. Node 4 reports nodes 21 to 23, which take their place among the gamma closest nodes. Node 23 reports a storing node.
	storingNode := &Node{ID: testID(t)}
	var storing []*SearchResult

	search, requests, elapsed := testLookupBlockchainNodes(t, key, nodes[:20], 200*time.Millisecond, func(node *Node) (closest, storing []*Node, ok bool, delay time.Duration) {
		switch {
		case isNode(node, 1), isNode(node, 2), isNode(node, 3):
			return nil, nil, false, 0
		case isNode(node, 4):
			return nodes[20:23], nil, true, 0
		case isNode(node, 23):
			return nil, []*Node{storingNode}, true, 0
		}
		return nil, nil, true, 0
	}, func(search *SearchClient, result *SearchResult) {
		storing = append(storing, result)
	})

	for number, node := range nodes {
		if count := requests[string(node.ID)]; count != 1 {
			t.Errorf("closest nodes do not answer: node %d was sent %d requests, expected 1", number+1, count)
		}
		if answered := search.answered[string(node.ID)]; answered != (number >= 3) {
			t.Errorf("closest nodes do not answer: node %d answered %t", number+1, answered)
		}
	}
	if len(search.unanswered) != 3 {
		t.Errorf("closest nodes do not answer: %d nodes are recorded as not answering, expected 3", len(search.unanswered))
	}
	if len(storing) != 1 || storing[0].StoringNode != storingNode || !isNode(&Node{ID: storing[0].SenderID}, 23) {
		t.Errorf("closest nodes do not answer: %d storing nodes handed over, expected the one that node 23 reports", len(storing))
	}
	if elapsed > time.Second {
		t.Errorf("closest nodes do not answer: search ended after %s", elapsed)
	}

	// A network of 8 nodes: every node answers and reports the others. No node is left to ask, the search ends at once.
	storing = nil

	search, requests, elapsed = testLookupBlockchainNodes(t, key, nodes[:8], time.Second, func(node *Node) (closest, storing []*Node, ok bool, delay time.Duration) {
		if isNode(node, 8) {
			return nodes[:5], []*Node{storingNode}, true, 0
		}
		return nodes[3:8], nil, true, 0
	}, func(search *SearchClient, result *SearchResult) {
		storing = append(storing, result)
	})

	if len(requests) != 8 || len(search.answered) != 8 {
		t.Errorf("8 nodes: %d nodes were sent the request and %d answered, expected 8", len(requests), len(search.answered))
	}
	for number, node := range nodes[:8] {
		if count := requests[string(node.ID)]; count != 1 {
			t.Errorf("8 nodes: node %d was sent %d requests, expected 1", number+1, count)
		}
	}
	if len(storing) != 1 || storing[0].StoringNode != storingNode {
		t.Errorf("8 nodes: %d storing nodes handed over, expected the one that node 8 reports", len(storing))
	}
	if elapsed > time.Second/2 {
		t.Errorf("8 nodes: search ended after %s", elapsed)
	}

	// No node answers. Every node of the routing table is sent the request, then no node is left to ask.
	_, requests, elapsed = testLookupBlockchainNodes(t, key, nodes[:8], 100*time.Millisecond, func(node *Node) (closest, storing []*Node, ok bool, delay time.Duration) {
		return nil, nil, false, 0
	}, func(search *SearchClient, result *SearchResult) {
		t.Errorf("no node answers: storing node %x handed over", result.StoringNode.ID)
	})

	if len(requests) != 8 {
		t.Errorf("no node answers: %d nodes were sent the request, expected 8", len(requests))
	}
	if elapsed > time.Second {
		t.Errorf("no node answers: search ended after %s", elapsed)
	}

	// An empty routing table: the search ends without a request.
	_, requests, elapsed = testLookupBlockchainNodes(t, key, nil, time.Second, func(node *Node) (closest, storing []*Node, ok bool, delay time.Duration) {
		return nil, nil, true, 0
	}, func(search *SearchClient, result *SearchResult) {})

	if len(requests) != 0 || elapsed > time.Second/2 {
		t.Errorf("empty routing table: %d nodes were sent the request, search ended after %s", len(requests), elapsed)
	}
}

// TestLookupBlockchainTerminate checks a search for the nodes that store a blockchain that the caller ends when the first storing node is handed over.
// It runs in a synctest bubble: the delays of the answers and the waiting pass on the clock of the bubble.
func TestLookupBlockchainTerminate(t *testing.T) {
	synctest.Test(t, testLookupBlockchainTerminate)
}

func testLookupBlockchainTerminate(t *testing.T) {
	key := testID(t)

	var nodes []*Node
	for distance := uint32(1); distance <= 20; distance++ {
		nodes = append(nodes, &Node{ID: testIDDistance(key, distance)})
	}

	dht := NewDHT(&Node{ID: testID(t)}, 256, 20, 5, 5, 20)
	for _, node := range nodes {
		dht.AddNode(node)
	}

	// Every node answers after 30 ms and reports two storing nodes.
	var requests int32
	var answers sync.WaitGroup

	dht.SendRequest = func(request *InformationRequest) {
		for _, node := range request.Nodes {
			atomic.AddInt32(&requests, 1)

			message := &NodeMessage{SenderID: node.ID, Storing: []*Node{{ID: testID(t)}, {ID: testID(t)}}}

			answers.Go(func() {
				time.Sleep(30 * time.Millisecond)
				request.QueueResult(message)
				request.Done()
			})
		}
	}

	dht.TimeoutSearch, dht.TimeoutIR = 2*time.Second, time.Second

	search, err := dht.FindBlockchain(key)
	if err != nil {
		t.Fatal(err)
	}

	// When the test ends, the search is ended and the answers under way are waited for: a synctest bubble ends with a panic if a goroutine is left.
	defer func() {
		search.Terminate()
		answers.Wait()
	}()

	select {
	case result := <-search.Storing:
		if result.StoringNode == nil {
			t.Fatal("storing node missing in the result")
		}
	case <-search.TerminateSignal:
		t.Fatal("search ended before a storing node was handed over")
	}

	search.Terminate()
	search.Terminate()

	time.Sleep(50 * time.Millisecond)
	requestsEnd := atomic.LoadInt32(&requests)

	time.Sleep(300 * time.Millisecond)

	if count := atomic.LoadInt32(&requests); count != requestsEnd || count >= int32(len(nodes)) {
		t.Fatalf("%d requests sent, %d of them after the search was ended by the caller", count, count-requestsEnd)
	}
	if _, ok := <-search.Results; ok {
		t.Fatal("unexpected result")
	}
}

// TestLookupBlockchainClosest checks the count of closest nodes that a search for the nodes that store a blockchain accepts from one node: gamma in total.
func TestLookupBlockchainClosest(t *testing.T) {
	key := testID(t)
	nodeA := &Node{ID: testIDDistance(key, 1000)}

	// Node A reports 30 nodes in 3 results of 10. The nodes it reports answer without reporting a node.
	var reported []*Node
	for distance := uint32(1); distance <= 30; distance++ {
		reported = append(reported, &Node{ID: testIDDistance(key, distance)})
	}

	dht := NewDHT(&Node{ID: testID(t)}, 256, 20, 5, 5, 20)
	dht.AddNode(nodeA)

	requests := make(map[string]int)

	dht.SendRequest = func(request *InformationRequest) {
		for _, node := range request.Nodes {
			requests[string(node.ID)]++

			if bytes.Equal(node.ID, nodeA.ID) {
				request.QueueResult(&NodeMessage{SenderID: node.ID, Closest: reported[20:30]})
				request.QueueResult(&NodeMessage{SenderID: node.ID, Closest: reported[10:20]})
				request.QueueResult(&NodeMessage{SenderID: node.ID, Closest: reported[0:10]})
			} else {
				request.QueueResult(&NodeMessage{SenderID: node.ID})
			}
			request.Done()
		}
	}

	search := dht.NewSearch(ActionFindBlockchain, key, 2*time.Second, time.Second, 5)
	search.SearchAway()
	testCollectStoring(search)

	// The nodes of the first two results are accepted and sent the request, the nodes of the third result are left out.
	if len(requests) != 21 || len(search.list.Nodes) != 21 {
		t.Fatalf("%d nodes were sent the request and %d nodes are in the search list, expected 21", len(requests), len(search.list.Nodes))
	}
	for number, node := range reported {
		if count := requests[string(node.ID)]; (count == 1) != (number >= 10) {
			t.Errorf("reported node %d was sent %d requests", number+1, count)
		}
	}
}

// TestLookupNodeStoring checks that a search for a node takes no storing nodes from an answer, and that a search for the nodes that store a blockchain does not end when the node with the ID of the key answers.
func TestLookupNodeStoring(t *testing.T) {
	key := testID(t)
	nodeKey := &Node{ID: key}
	nodeA := &Node{ID: testIDDistance(key, 8)}
	nodeB := &Node{ID: testIDDistance(key, 9)}

	answer := func(request *InformationRequest) {
		for _, node := range request.Nodes {
			request.QueueResult(&NodeMessage{SenderID: node.ID, Closest: []*Node{nodeA, nodeB}, Storing: []*Node{{ID: testID(t)}}})
			request.Done()
		}
	}

	// A search for a node that no node reports: the answers hold storing nodes.
	dht := NewDHT(&Node{ID: testID(t)}, 256, 20, 5, 5, 20)
	dht.AddNode(nodeA)
	dht.AddNode(nodeB)
	dht.SendRequest = answer

	timeStart := time.Now()
	search := dht.NewSearch(ActionFindNode, testIDDistance(key, 1), 2*time.Second, time.Second, 5)
	search.SearchAway()

	if storing := testCollectStoring(search); len(storing) != 0 {
		t.Fatalf("search for a node: %d storing nodes handed over", len(storing))
	}
	if elapsed := time.Since(timeStart); elapsed > time.Second/2 {
		t.Fatalf("search for a node: search ended after %s", elapsed)
	}

	// A search for a blockchain: the node with the ID of the key is in the routing table and answers. Nodes A and B are sent the request as well.
	dht = NewDHT(&Node{ID: testID(t)}, 256, 20, 5, 5, 20)
	dht.AddNode(nodeKey)
	dht.AddNode(nodeA)
	dht.AddNode(nodeB)
	dht.SendRequest = answer

	search = dht.NewSearch(ActionFindBlockchain, key, 2*time.Second, time.Second, 5)
	search.SearchAway()

	if storing := testCollectStoring(search); len(storing) != 3 {
		t.Fatalf("search for a blockchain: %d storing nodes handed over, expected 3", len(storing))
	}
	if result, ok := <-search.Results; ok {
		t.Fatalf("search for a blockchain: unexpected result from %x", result.SenderID)
	}
	if len(search.answered) != 3 {
		t.Fatalf("search for a blockchain: %d nodes answered, expected 3", len(search.answered))
	}

	// A search for a blockchain: node A reports the node with the ID of the key. It is sent the request in the next information request with the 4 closest nodes not yet contacted.
	dht = NewDHT(&Node{ID: testID(t)}, 256, 20, 5, 5, 20)
	for distance := uint32(8); distance < 18; distance++ {
		dht.AddNode(&Node{ID: testIDDistance(key, distance)})
	}

	var requests [][]*Node

	dht.SendRequest = func(request *InformationRequest) {
		requests = append(requests, request.Nodes)

		for _, node := range request.Nodes {
			message := &NodeMessage{SenderID: node.ID}
			if bytes.Equal(node.ID, nodeA.ID) {
				message.Closest = []*Node{nodeKey}
			}
			request.QueueResult(message)
			request.Done()
		}
	}

	search, err := dht.FindBlockchain(key)
	if err != nil {
		t.Fatal(err)
	}
	testCollectStoring(search)

	if len(requests) != 3 || len(requests[0]) != 5 || len(requests[1]) != 5 || len(requests[2]) != 1 || !bytes.Equal(requests[1][0].ID, key) {
		t.Fatalf("search for a blockchain: %d information requests, the node with the ID of the key is not in the second one of 5 nodes", len(requests))
	}

	if _, err := dht.FindBlockchain(key[:31]); err == nil {
		t.Fatal("search for a blockchain: key of 31 bytes accepted")
	}
}
