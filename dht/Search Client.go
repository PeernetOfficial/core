/*
File Name:  Search Client.go
Copyright:  2021 Kleissner Ventures Ltd
Author:     Peter Kleissner

A search client runs concurrent information requests for a single query. It solves the query efficiently by using levels.
Any result that is closer to the target gets pushed down into a new lower level, which contacts nodes closer to the result.
Level are running concurrently.
*/

package dht

import (
	"bytes"
	"encoding/hex"
	"sort"
	"sync"
	"time"
)

// MaxAcceptKnownStore is maximum count accepted of known peers that store the value
const MaxAcceptKnownStore = 10

// MaxClosest is maximum number of closest peers accepted
const MaxClosest = 5

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
	Results             chan *SearchResult                              // Result channel. It is closed when the search is terminated.
	list                *shortList                                      // List of nodes to contact
	contactedNodesMap   map[string]struct{}                             // List of nodes already contacted
	contactedNodesMutex sync.RWMutex                                    // Sync map access
	storing             chan []*Node                                    // Internal channel to signal nodes that indicate storing the searched value.
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
		Results:           make(chan *SearchResult, 1),
		LogStatus:         func(function, format string, v ...interface{}) {},
	}

	return
}

// Terminate sends the termination signal to all workers. It is safe to call Terminate multiple times.
func (client *SearchClient) Terminate() {
	client.Lock()
	defer client.Unlock()

	client.terminate()
}

// terminate sends the termination signal to all workers. The caller holds the lock.
func (client *SearchClient) terminate() {
	if client.IsTerminated {
		return
	}

	// set the termination signal
	client.IsTerminated = true
	close(client.TerminateSignal) // safety guaranteed via lock

	client.timeEnd = time.Now()

	close(client.Results)
}

// sendResult queues the result and terminates the search. A search has one result: if the search is terminated already, the result is discarded.
func (client *SearchClient) sendResult(result *SearchResult) {
	client.Lock()
	defer client.Unlock()

	if client.IsTerminated {
		return
	}

	client.Results <- result
	client.terminate()
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
	go client.startSearch()

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

	// A node that is sent the request is not sent it again when it is later reported as storing the value, by itself or by another node.
	for _, node := range nodes {
		client.isContactedNode(node.ID, true)
		client.LogStatus("search.sendInfoRequest", "contact node %s\n", hex.EncodeToString(node.ID))
	}

	info = client.dht.NewInformationRequest(client.Action, client.Key, nodes)
	info.ResultChanExt = resultChan

	client.dht.SendRequest(info)

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
				client.sendResult(&SearchResult{Key: client.Key, Action: client.Action, SenderID: result.SenderID, Data: result.Data})
				return
			}
		}
	}
}

// startSearch runs the search. Each level sends an information request to the alpha closest nodes not yet contacted.
// The next level starts as soon as a result brings a node that is closer to the key than the closest node contacted.
func (client *SearchClient) startSearch() {
	results := make(chan *NodeMessage, messageChannelSize)
	levelEnd := make(chan struct{})
	level, activeLevels := 0, 0

	// startLevel sends an information request to the closest nodes not yet contacted.
	startLevel := func() (started bool) {
		nodes := client.list.GetUncontacted(client.alpha, true)
		if len(nodes) == 0 {
			return false
		}

		client.LogStatus("search.startSearch", "start search in level %d contacting %d nodes\n", level, len(nodes))

		info := client.sendInfoRequest(nodes, results)
		if info == nil {
			return false
		}

		activeLevels++

		go func() {
			<-info.TerminateSignal

			select {
			case levelEnd <- struct{}{}:
			case <-client.TerminateSignal:
			}
		}()

		return true
	}

	// handleResult handles the result of a node. It returns true if the search is terminated.
	handleResult := func(result *NodeMessage) (terminated bool) {
		switch client.Action {
		case ActionFindValue:
			// search for value and it was found?
			if len(result.Data) > 0 {
				client.LogStatus("search.startSearch", "result: sender %s: data found (%d bytes)\n", hex.EncodeToString(result.SenderID), len(result.Data))
				client.sendResult(&SearchResult{Key: client.Key, Action: client.Action, SenderID: result.SenderID, Data: result.Data})
				return true
			}

			result.Storing = client.filterUncontactedNodes(result.Storing, MaxAcceptKnownStore)
			result.Closest = client.filterUncontactedNodes(result.Closest, MaxClosest)

			client.LogStatus("search.startSearch", "result: sender %s: %d uncontacted nodes store and %d nodes are close to value\n", hex.EncodeToString(result.SenderID), len(result.Storing), len(result.Closest))

			// Find value: Nodes known to store the value are queried in a separate function.
			if len(result.Storing) > 0 {
				select {
				case client.storing <- result.Storing:
				case <-client.TerminateSignal:
					return true
				}
			}

		case ActionFindNode:
			// search for node and it was found?
			for _, closePeer := range result.Closest {
				if bytes.Equal(closePeer.ID, client.Key) {
					client.LogStatus("search.startSearch", "result: sender %s: node found!\n", hex.EncodeToString(result.SenderID))
					client.sendResult(&SearchResult{Key: client.Key, Action: client.Action, SenderID: result.SenderID, TargetNode: closePeer})
					return true
				}
			}

			result.Closest = client.filterUncontactedNodes(result.Closest, MaxClosest)

			client.LogStatus("search.startSearch", "find node: sender %s: %d nodes are close to value\n", hex.EncodeToString(result.SenderID), len(result.Closest))

		case ActionFindValueClosest:
			// Only the nodes close to the key are of interest.
			result.Closest = client.filterUncontactedNodes(result.Closest, MaxClosest)

			client.LogStatus("search.startSearch", "find closest: sender %s: %d nodes are close to value\n", hex.EncodeToString(result.SenderID), len(result.Closest))
		}

		// Add closest to list. The list is ordered by distance to the key, closest first.
		client.list.AppendUniqueNodes(result.Closest...)
		sort.Sort(client.list)

		// If the result brought a node closer to the key than the closest node contacted, start the next level.
		if level < MaxLevel && !client.list.Contacted[string(client.list.Nodes[0].ID)] {
			level++
			startLevel()
		}

		return false
	}

	if !startLevel() {
		client.Terminate()
		return
	}

	for {
		select {
		case <-client.TerminateSignal:
			client.LogStatus("search.startSearch", "search aborted, search client termination signal\n")
			return

		case result := <-results:
			if handleResult(result) {
				return
			}

		case <-levelEnd:
			activeLevels--

			// Results that were queued before the information request ended are handled first.
		resultsLoop:
			for {
				select {
				case result := <-results:
					if handleResult(result) {
						return
					}
				default:
					break resultsLoop
				}
			}

			if activeLevels > 0 {
				client.LogStatus("search.startSearch", "level end, info request termination signal\n")
				continue
			}

			// No level is active and no result brought a node closer to the key than the closest node contacted. This is the end of a search for a node.
			// A search for a value continues with the closest nodes not yet contacted.
			if client.Action != ActionFindNode && startLevel() {
				continue
			}

			client.LogStatus("search.startSearch", "last active level ended, not found, terminate search\n")
			client.Terminate()
			return
		}
	}
}
