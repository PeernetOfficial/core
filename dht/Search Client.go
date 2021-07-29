/*
File Name:  Search Client.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

A search client runs concurrent information requests for a single query. It solves the query efficiently by using levels.
Any result that is closer to the target gets pushed down into a new lower level, which contacts nodes closer to the result.
Level are running concurrently.
*/

package dht

import (
	"bytes"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"
)

// MaxAcceptKnownStore is maximum count accepted of known peers that store the value
const MaxAcceptKnownStore = 10

// MaxClosest is maximum number of closest peers accepted
const MaxClosest = 3

// MaxLevel defines the max level.
const MaxLevel = 10

// SearchClient defines a search in the distributed hash table involving multiple information requests.
// The search can be created for a node or for a value, both identified by the hash (known as the key).
type SearchClient struct {
	Action              int                                             // ActionX
	Key                 []byte                                          // Key that is being queried
	IsTerminated        bool                                            // If true, it was signaled for termination
	TerminateSignal     chan struct{}                                   // gets closed on termination signal, can be used in select via "case _ = <- TerminateSignal:"
	sync.Mutex                                                          // for sychronized closing
	timeStart           time.Time                                       // When the search started.
	timeEnd             time.Time                                       // When the search ended.
	dht                 *DHT                                            // DHT used
	timeoutTotal        time.Duration                                   // Timeout after the entire search will be terminated client-side.
	timeoutIR           time.Duration                                   // Timeout for information requests (entire roundtrip).
	alpha               int                                             // Count of concurrent information requests per level.
	Results             chan *SearchResult                              // Result channel
	list                *shortList                                      // List of nodes to contact
	contactedNodesMap   map[string]struct{}                             // List of nodes already contacted
	contactedNodesMutex sync.RWMutex                                    // Sync map access
	storing             chan []*Node                                    // Internal channel to signal nodes that indicate storing the searched value.
	activeLevels        uint64                                          // demo
	LogStatus           func(function, format string, v ...interface{}) // Filter function for status output
}

// SearchResult is a single result to the search. Depending on the search type and parameters, multiple results may be sent.
type SearchResult struct {
	Key      []byte // Original key that was searched for
	Action   int    // Original action
	SenderID []byte // Sender node ID of the result

	// data for ActionFindNode
	TargetNode *Node // The node that was requested.

	// data for ActionFindValue
	Data []byte // Actual data
}

// NewSearch creates a new search client.
// Action indicates the action to take (from ActionX constants), to either find a node, or a value.
// Timeout is the total time the search may take, covering all information requests. TimeoutIR is the time an information request may take.
// Alpha is the number of concurrent requests that will be performed.
func (dht *DHT) NewSearch(Action int, Key []byte, Timeout, TimeoutIR time.Duration, Alpha int) (client *SearchClient) {
	client = &SearchClient{
		Action:            Action,
		Key:               Key,
		dht:               dht,
		timeoutTotal:      Timeout,
		timeoutIR:         TimeoutIR,
		alpha:             Alpha,
		contactedNodesMap: make(map[string]struct{}),
		storing:           make(chan []*Node, Alpha*2),
		TerminateSignal:   make(chan struct{}),
		Results:           make(chan *SearchResult),
		LogStatus:         func(function, format string, v ...interface{}) {},
	}

	return
}

// Terminate sends the termination signal to all workers. It is safe to call Terminate multiple times.
func (client *SearchClient) Terminate() {
	client.Lock()
	defer client.Unlock()

	if client.IsTerminated {
		return
	}

	// set the termination signal
	client.IsTerminated = true
	close(client.TerminateSignal) // safety guaranteed via lock

	client.timeEnd = time.Now()

	close(client.Results)
	close(client.storing)
}

// isContactedNode checks if a node was contacted
func (client *SearchClient) isContactedNode(ID []byte, Set bool) (contacted bool) {
	client.contactedNodesMutex.Lock()
	_, contacted = client.contactedNodesMap[string(ID)]
	if Set {
		client.contactedNodesMap[string(ID)] = struct{}{}
	}
	client.contactedNodesMutex.Unlock()
	return contacted
}

// filterUncontactedNodes returns only nodes that were not contacted so far. All nodes will be set to contacted. Limit is optional (0 for no limit).
func (client *SearchClient) filterUncontactedNodes(input []*Node, limit int) (output []*Node) {
	client.contactedNodesMutex.Lock()

	for _, node := range input {
		if _, ok := client.contactedNodesMap[string(node.ID)]; !ok {
			output = append(output, node)
			client.contactedNodesMap[string(node.ID)] = struct{}{}

			if limit > 0 {
				limit--
				if limit == 0 {
					break
				}
			}
		}
	}

	client.contactedNodesMutex.Unlock()
	return output
}

// SearchAway starts the search. Non-blocking!
func (client *SearchClient) SearchAway() {
	client.timeStart = time.Now()

	// create the first search level and start it
	client.list = client.dht.ht.getClosestContacts(client.alpha, client.Key, nil)
	if len(client.list.Nodes) == 0 {
		client.Terminate()
		return
	}

	go client.queryNodesKnownStore()

	// start the first information request
	go client.startSearch(0)

	// start an automated termination function for the timeout
	go func(client *SearchClient) {
		// sleep + watch for closing
		select {
		case <-client.TerminateSignal: // exit the function on other signal
			return
		case <-time.After(client.timeoutTotal):
			client.Terminate()
		}
	}(client)
}

// sendInfoRequest sends out a new info request to the nodes
func (client *SearchClient) sendInfoRequest(nodes []*Node, resultChan chan *NodeMessage) (info *InformationRequest) {
	if client.IsTerminated {
		return nil
	}

	for _, node := range nodes {
		client.LogStatus("search.sendInfoRequest", "contact node %s\n", hex.EncodeToString(node.ID))
	}

	info = client.dht.NewInformationRequest(client.Action, client.Key, nodes)
	info.ResultChanExt = resultChan

	switch client.Action {
	case ActionFindNode:
		client.dht.SendRequestFindNode(info)
	case ActionFindValue:
		client.dht.SendRequestFindValue(info)
	}

	go func() {
		select {
		case <-client.TerminateSignal:
		case <-time.After(client.timeoutIR):
		}
		info.Terminate()
	}()

	return info
}

// queryNodesKnownStore queries nodes that are known to store the value. Only for ActionFindValue.
// Returned 'closest nodes' are ignored, as the queried nodes are expected to store the value. This might be adjusted in the future.
func (client *SearchClient) queryNodesKnownStore() {
	// all results are redirected to a single channel
	resultChan := make(chan *NodeMessage, client.alpha)

	for {
		select {
		case <-client.TerminateSignal:
			return

		case nodes := <-client.storing:
			client.sendInfoRequest(nodes, resultChan)

		case result := <-resultChan:
			if len(result.Data) > 0 {
				client.Results <- &SearchResult{Key: client.Key, Action: client.Action, SenderID: result.SenderID, Data: result.Data}
				client.Terminate()
				return
			}
		}
	}
}

func (client *SearchClient) startSearch(level int) {
	atomic.AddUint64(&client.activeLevels, 1)
	defer atomic.AddUint64(&client.activeLevels, ^uint64(0))
	nestedStarted := false

	results := make(chan *NodeMessage, client.alpha*2)

	closestNode := client.list.Nodes[0]

	// start an info request
	startInfoRequest := func() (info *InformationRequest) {
		nodes := client.list.GetUncontacted(client.alpha, true)
		if len(nodes) == 0 {
			client.LogStatus("search.startSearch", "search in level %d aborted, no new nodes to contact\n", level)
			return nil
		}
		client.LogStatus("search.startSearch", "start search in level %d contacting %d nodes\n", level, len(nodes))
		return client.sendInfoRequest(nodes, results)
	}

	info := startInfoRequest()
	if info == nil {
		return
	}

	for {
		select {
		case <-client.TerminateSignal:
			client.LogStatus("search.startSearch", "search in level %d aborted, search client termination signal\n", level)
			return

		case result := <-results:

			switch client.Action {
			case ActionFindValue:
				// search for value and it was found?
				if len(result.Data) > 0 {
					client.LogStatus("search.startSearch", "result: sender %s: data found (%d bytes)\n", hex.EncodeToString(result.SenderID), len(result.Data))
					client.Results <- &SearchResult{Key: client.Key, Action: client.Action, SenderID: result.SenderID, Data: result.Data}
					client.Terminate()
					return
				}

				result.Storing = client.filterUncontactedNodes(result.Storing, MaxAcceptKnownStore)
				result.Closest = client.filterUncontactedNodes(result.Closest, MaxClosest)

				client.LogStatus("search.startSearch", "result: sender %s: %d uncontacted nodes store and %d nodes are close to value\n", hex.EncodeToString(result.SenderID), len(result.Storing), len(result.Closest))

				// Find value: Nodes known to store the value are queried in a separate function.
				if len(result.Storing) > 0 {
					client.storing <- result.Storing
				}

			case ActionFindNode:
				// search for node and it was found?
				for _, closePeer := range result.Closest {
					if bytes.Equal(closePeer.ID, client.Key) {
						client.LogStatus("search.startSearch", "result: sender %s: node found!\n", hex.EncodeToString(result.SenderID))
						client.Results <- &SearchResult{Key: client.Key, Action: client.Action, SenderID: result.SenderID, TargetNode: closePeer}
						client.Terminate()
						return
					}
				}

				result.Closest = client.filterUncontactedNodes(result.Closest, MaxClosest)

				client.LogStatus("search.startSearch", "find node: sender %s: %d nodes are close to value\n", hex.EncodeToString(result.SenderID), len(result.Closest))

			}

			// Add closest to list
			client.list.AppendUniqueNodes(result.Closest...)

			// If no subsequent level, and there's closer nodes, start one!
			if !nestedStarted && !bytes.Equal(client.list.Nodes[0].ID, closestNode.ID) && level < MaxLevel {
				nestedStarted = true
				go client.startSearch(level + 1)
			}

		case <-info.TerminateSignal:
			// If highest level (= not nested), and there was no conclusive result, try one more round.
			// This helps against result poisoning.

			if !nestedStarted {
				client.LogStatus("search.startSearch", "search in level %d aborted, info request termination signal. Final try.\n", level)
				if info = startInfoRequest(); info != nil {
					continue
				}
			}

			if client.activeLevels == 1 { // if this was the last level, no more results will appear
				client.LogStatus("search.startSearch", "level %d last active level, not found, terminate search\n", level)
				client.Terminate()
			} else {
				client.LogStatus("search.startSearch", "level %d end, info request termination signal\n", level)
			}
			return

			//case <-time.After(time.Second):
			// Future todo: Launch another routine with the with uncontacted nodes if any, to speed up the query
		}
	}
}
