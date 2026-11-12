/*
File Name:  Search Client_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package dht

import (
	"crypto/rand"
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

	dht := NewDHT(&Node{ID: testID(t)}, 256, 20, 5)
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
