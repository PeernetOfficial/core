/*
File Name:  Search Client_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package dht

import (
	"crypto/rand"
	"strings"
	"sync"
	"testing"
	"time"
)

func testID(t *testing.T) []byte {
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestSearchStoringNodes checks which nodes a search for a value queries when nodes are reported as storing the value:
// a node that was not sent the request yet is sent it, a node that was sent the request already is not sent it again.
func TestSearchStoringNodes(t *testing.T) {
	nodeA := &Node{ID: testID(t)} // stores the value and lists itself as storing node
	nodeB := &Node{ID: testID(t)} // reports that nodeC and nodeA store the value
	nodeC := &Node{ID: testID(t)} // only known as storing node via nodeB
	nodeD := &Node{ID: testID(t)} // does not answer, which keeps the first information request open

	dht := NewDHT(&Node{ID: testID(t)}, 256, 20, 5, 5)
	dht.AddNode(nodeA)
	dht.AddNode(nodeB)
	dht.AddNode(nodeD)

	var mutex sync.Mutex
	requests := make(map[string]int)

	// Every node answers without the data: the value does not fit into the response.
	dht.SendRequest = func(request *InformationRequest) {
		for _, node := range request.Nodes {
			mutex.Lock()
			requests[string(node.ID)]++
			mutex.Unlock()

			message := &NodeMessage{SenderID: node.ID}
			switch string(node.ID) {
			case string(nodeA.ID):
				message.Storing = []*Node{nodeA}
			case string(nodeB.ID):
				message.Storing = []*Node{nodeC, nodeA}
			case string(nodeC.ID):
				message.Storing = []*Node{nodeC}
			case string(nodeD.ID):
				continue
			}

			go func() {
				time.Sleep(20 * time.Millisecond)
				request.QueueResult(message)
				request.Done()
			}()
		}
	}

	search := dht.NewSearch(ActionFindValue, testID(t), 2*time.Second, 300*time.Millisecond, 5)
	search.SearchAway()

	for result := range search.Results {
		t.Fatalf("unexpected search result from %x", result.SenderID)
	}

	mutex.Lock()
	defer mutex.Unlock()

	for name, node := range map[string]*Node{"A": nodeA, "B": nodeB, "C": nodeC, "D": nodeD} {
		if count := requests[string(node.ID)]; count != 1 {
			t.Errorf("node %s received %d requests, expected 1", name, count)
		}
	}
}

// TestSearchTerminate checks a search that is terminated while it hands over what it found.
func TestSearchTerminate(t *testing.T) {
	target := &Node{ID: testID(t)}
	nodeA := &Node{ID: testID(t)}

	dht := NewDHT(&Node{ID: testID(t)}, 256, 20, 5, 5)
	dht.AddNode(nodeA)

	// A search for a node: the node is reported, the timeout of the search passes before the result is read.
	dht.SendRequest = func(request *InformationRequest) {
		request.QueueResult(&NodeMessage{SenderID: nodeA.ID, Closest: []*Node{target}})
		request.Done()
	}

	search := dht.NewSearch(ActionFindNode, target.ID, 100*time.Millisecond, 50*time.Millisecond, 5)
	search.SearchAway()

	time.Sleep(300 * time.Millisecond)

	if result, ok := <-search.Results; !ok {
		t.Fatal("search for a node: no result")
	} else if string(result.TargetNode.ID) != string(target.ID) {
		t.Fatalf("search for a node: result is node %x", result.TargetNode.ID)
	}
	if _, ok := <-search.Results; ok {
		t.Fatal("search for a node: second result")
	}

	// A search for a value: the search is terminated right before a storing node is handed over.
	dht.SendRequest = func(request *InformationRequest) {
		request.QueueResult(&NodeMessage{SenderID: nodeA.ID, Storing: []*Node{target}})
		request.Done()
	}

	search = dht.NewSearch(ActionFindValue, testID(t), 2*time.Second, 300*time.Millisecond, 5)
	search.LogStatus = func(function, format string, v ...interface{}) {
		if strings.Contains(format, "uncontacted nodes store") {
			search.Terminate()
		}
	}
	search.SearchAway()

	if result, ok := <-search.Results; ok {
		t.Fatalf("search for a value: unexpected result from %x", result.SenderID)
	}

	time.Sleep(100 * time.Millisecond)
}
