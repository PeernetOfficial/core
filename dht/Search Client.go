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

// MaxClosest is maximum number of closest peers accepted from a response record in a search for a node. A response record carries at most 5.
// In a search for the nodes that store a blockchain, gamma closest peers are accepted from a node in total.
const MaxClosest = 5

// MaxRequests is the maximum count of nodes a search sends the request to. It bounds a search in which nodes keep reporting closer nodes.
const MaxRequests = 100

// SearchClient defines a search in the distributed hash table involving multiple information requests.
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
	beta                int                                             // ActionFindNode: Count of closest nodes that must have answered for the search to end.
	gamma               int                                             // ActionFindBlockchain: Count of closest nodes that must have answered for the search to end.
	answered            map[string]bool                                 // Nodes that answered.
	closestAccepted     map[string]int                                  // ActionFindBlockchain: Count of closest nodes accepted from each node.
	unanswered          map[string]bool                                 // Nodes that did not answer by the end of their information request.
	target              []*Node                                         // ActionFindNode: The node searched for, as known from the routing table or reported in a result. Each one after the first holds addresses the ones before do not.
	targetActive        int                                             // ActionFindNode: Count of information requests to the node searched for that are under way.
	Results             chan *SearchResult                              // Result channel. It is closed when the search is terminated.
	Storing             chan *SearchResult                              // ActionFindBlockchain: Each storing node that a node reports. The search waits until the caller has read it; after TerminateSignal no more follow.
	list                *shortList                                      // List of nodes to contact
	contactedNodesMap   map[string]struct{}                             // List of nodes already contacted
	contactedNodesMutex sync.RWMutex                                    // Sync map access
	LogStatus           func(function, format string, v ...interface{}) // Filter function for status output
}

// SearchResult is a single result to the search. Depending on the search type and parameters, multiple results may be sent.
type SearchResult struct {
	Key      []byte // Original key that was searched for
	Action   int    // Original action
	SenderID []byte // Sender node ID of the result

	// data for ActionFindNode
	TargetNode *Node // The node that was requested. It answered the request.

	// data for ActionFindBlockchain
	StoringNode *Node // A node that stores the blockchain of the key, as reported by the sender.
}

// NewSearch creates a new search client.
// Action indicates the action to take (from ActionX constants).
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
		beta:              dht.beta,
		gamma:             dht.gamma,
		answered:          make(map[string]bool),
		closestAccepted:   make(map[string]int),
		unanswered:        make(map[string]bool),
		contactedNodesMap: make(map[string]struct{}),
		TerminateSignal:   make(chan struct{}),
		Results:           make(chan *SearchResult, 1),
		Storing:           make(chan *SearchResult),
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

// sendStoring hands a storing node to the caller and waits until it is read or the search is terminated.
func (client *SearchClient) sendStoring(result *SearchResult) {
	select {
	case client.Storing <- result:
	case <-client.TerminateSignal:
	}
}

// closestEnd returns the count of closest nodes that must have answered for the search to end.
func (client *SearchClient) closestEnd() int {
	if client.Action == ActionFindBlockchain {
		return client.gamma
	}

	return client.beta
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

	// The search list starts with a full bucket size of the closest nodes of the routing table:
	// a node that does not answer is replaced by the next closest one.
	client.list = client.dht.ht.getClosestContacts(client.dht.ht.bSize, client.Key, nil)
	if len(client.list.Nodes) == 0 {
		client.Terminate()
		return
	}

	if bytes.Equal(client.list.Nodes[0].ID, client.Key) {
		client.target = append(client.target, client.list.Nodes[0])
	}

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

// startSearch runs the search. Each level sends an information request to the alpha closest nodes not yet contacted.
// A search for a node starts the next level as soon as one of the beta closest nodes is not yet contacted, and ends when they have all answered.
// A node that does not answer is not counted among the beta. The node searched for is sent the request as soon as a result reports it, and is found when it answers.
// If a later result reports it with addresses that are not yet tried, it is sent the request at those.
// A search for the nodes that store a blockchain proceeds the same way with the gamma closest nodes. It hands over each storing node that a result reports and ends when the gamma closest nodes have all answered.
func (client *SearchClient) startSearch() {
	results := make(chan *NodeMessage, messageChannelSize)
	levelEnd := make(chan []*Node)
	level, activeLevels, requests := 0, 0, 0

	// startRequest sends an information request to the nodes.
	startRequest := func(nodes []*Node) (started bool) {
		client.LogStatus("search.startSearch", "start search in level %d contacting %d nodes\n", level, len(nodes))

		info := client.sendInfoRequest(nodes, results)
		if info == nil {
			return false
		}

		activeLevels++
		requests += len(nodes)

		for _, node := range nodes {
			if bytes.Equal(node.ID, client.Key) {
				client.targetActive++
			}
		}

		go func() {
			<-info.TerminateSignal

			select {
			case levelEnd <- nodes:
			case <-client.TerminateSignal:
			}
		}()

		return true
	}

	// startLevel sends an information request to the closest nodes not yet contacted, to at most alpha and to as many as MaxRequests leaves.
	startLevel := func() (started bool) {
		count := client.alpha
		if count > MaxRequests-requests {
			count = MaxRequests - requests
		}
		nodes := client.list.GetUncontacted(count, true)
		if len(nodes) == 0 {
			return false
		}

		return startRequest(nodes)
	}

	// handleResult handles the result of a node. It returns true if the search is terminated.
	handleResult := func(result *NodeMessage) (terminated bool) {
		// search for node and it answered?
		if client.Action == ActionFindNode && result.Node != nil && bytes.Equal(result.SenderID, client.Key) {
			client.LogStatus("search.startSearch", "result: sender %s: node found!\n", hex.EncodeToString(result.SenderID))
			client.sendResult(&SearchResult{Key: client.Key, Action: client.Action, SenderID: result.SenderID, TargetNode: result.Node})
			return true
		}

		client.answered[string(result.SenderID)] = true

		// The storing nodes that are reported are handed over.
		if client.Action == ActionFindBlockchain {
			for _, storing := range result.Storing {
				client.LogStatus("search.startSearch", "result: sender %s: node %s stores the blockchain\n", hex.EncodeToString(result.SenderID), hex.EncodeToString(storing.ID))
				client.sendStoring(&SearchResult{Key: client.Key, Action: client.Action, SenderID: result.SenderID, StoringNode: storing})
			}
		}

		// The node searched for is reported: it is sent the request at once. If it is known already, it is sent the request at the addresses that are not yet tried.
		for _, closePeer := range result.Closest {
			if client.Action != ActionFindNode || !bytes.Equal(closePeer.ID, client.Key) || requests >= MaxRequests {
				continue
			}

			if len(client.target) == 0 {
				client.list.AppendUniqueNodes(closePeer)
				client.list.Contacted[string(closePeer.ID)] = true
			} else if closePeer = client.dht.NewNodeAddresses(client.target, closePeer); closePeer == nil {
				continue
			}

			client.target = append(client.target, closePeer)
			delete(client.unanswered, string(client.Key))

			level++
			startRequest([]*Node{closePeer})
		}

		if client.Action == ActionFindBlockchain {
			// A node reports at most gamma closest nodes, in one or more results.
			if limit := client.gamma - client.closestAccepted[string(result.SenderID)]; limit > 0 {
				result.Closest = client.filterUncontactedNodes(result.Closest, limit)
				client.closestAccepted[string(result.SenderID)] += len(result.Closest)
			} else {
				result.Closest = nil
			}
		} else {
			result.Closest = client.filterUncontactedNodes(result.Closest, MaxClosest)
		}

		client.LogStatus("search.startSearch", "find node: sender %s: %d nodes are close to value\n", hex.EncodeToString(result.SenderID), len(result.Closest))

		// Add closest to list. The list is ordered by distance to the key, closest first.
		client.list.AppendUniqueNodes(result.Closest...)
		sort.Sort(client.list)

		return false
	}

	// findNodeNext continues the search after a result or the end of a level. It returns true if the search is terminated.
	findNodeNext := func() (terminated bool) {
		// Check the beta closest nodes of the list (gamma in a search for the nodes that store a blockchain), without the nodes that did not answer.
		count, allAnswered, allContacted := 0, true, true

		for _, node := range client.list.Nodes {
			if client.unanswered[string(node.ID)] {
				continue
			}

			if !client.answered[string(node.ID)] {
				allAnswered = false
			}
			if !client.list.Contacted[string(node.ID)] {
				allContacted = false
			}

			if count++; count == client.closestEnd() {
				break
			}
		}

		if allAnswered {
			client.LogStatus("search.startSearch", "the %d closest nodes have all answered, terminate search\n", count)
			client.Terminate()
			return true
		}

		if !allContacted {
			level++
			startLevel()
		}

		if activeLevels == 0 {
			client.LogStatus("search.startSearch", "last active level ended, terminate search\n")
			client.Terminate()
			return true
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

			// The search continues once the results that arrived are all handled.
			if len(results) == 0 && findNodeNext() {
				return
			}

		case nodes := <-levelEnd:
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

			// The nodes of the level that did not answer are not counted among the beta closest nodes.
			// The node searched for did not answer once no information request to it is under way.
			for _, node := range nodes {
				if bytes.Equal(node.ID, client.Key) {
					if client.targetActive--; client.targetActive > 0 {
						continue
					}
				}

				if !client.answered[string(node.ID)] {
					client.unanswered[string(node.ID)] = true
				}
			}

			if findNodeNext() {
				return
			}
		}
	}
}
