/*
File Name:  Search Client_test.go
Copyright:  2026 Kleissner Ventures Ltd
Author:     Peter Kleissner
*/

package dht

import (
	"crypto/rand"
	"testing"
	"testing/synctest"
	"time"
)

func testID(t *testing.T) []byte {
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestSearchTerminate checks a search that is terminated while it hands over what it found.
// It runs in a synctest bubble: the timeout of the search passes on the clock of the bubble.
func TestSearchTerminate(t *testing.T) {
	synctest.Test(t, testSearchTerminate)
}

func testSearchTerminate(t *testing.T) {
	target := &Node{ID: testID(t)}

	dht := NewDHT(&Node{ID: testID(t)}, 256, 20, 5, 5, 20)
	dht.AddNode(&Node{ID: testID(t)})

	// A search for a node: the node is reported and answers, the timeout of the search passes before the result is read.
	dht.SendRequest = func(request *InformationRequest) {
		request.QueueResult(&NodeMessage{SenderID: request.Nodes[0].ID, Closest: []*Node{target}})
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
}
