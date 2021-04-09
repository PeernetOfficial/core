/*
File Name:  Information Request.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Information requests are asynchronous queries sent to nodes.
*/

package dht

import (
	"sync"
	"sync/atomic"
	"time"
)

// InformationRequest is an asynchronous request sent to nodes. It tracks any asynchronous replies and handles timeouts.
type InformationRequest struct {
	Action          int               // ActionX
	Key             []byte            // Key that is being queried
	ResultChan      chan *NodeMessage // Result channel
	ActiveNodes     uint64            // Number of nodes actively handling the request.
	Nodes           []*Node           // Nodes that are receiving the request.
	IsTerminated    bool              // If true, it was signaled for termination
	TerminateSignal chan struct{}     // gets closed on termination signal, can be used in select via "case _ = <- network.terminateSignal:"
	sync.Mutex                        // for sychronized closing
}

// Actions for performing the information request
const (
	ActionFindNode  = iota // Find a node
	ActionFindValue        // Find a value
)

// NewInformationRequest creates a new information request and adds it to the list.
// It marks the count of nodes as active, meaning the caller should later decrease it via ActiveNodesSub.
func (dht *DHT) NewInformationRequest(Action int, Key []byte, Nodes []*Node) (ir *InformationRequest) {
	ir = &InformationRequest{
		ResultChan:      make(chan *NodeMessage),
		TerminateSignal: make(chan struct{}),
		Action:          Action,
		Key:             Key,
		Nodes:           Nodes,
		ActiveNodes:     uint64(len(Nodes)),
	}

	for _, node := range Nodes {
		dht.irAdd(node.ID, ir)
	}

	return
}

// CollectResults collects all information request responses within the given timeout.
func (ir *InformationRequest) CollectResults(timeout time.Duration) (results []*NodeMessage) {
	for {
		select {
		case result := <-ir.ResultChan:
			results = append(results, result)

		case <-time.After(timeout):
			ir.Terminate()
			return

		case <-ir.TerminateSignal:
			return
		}
	}
}

// Terminate sends the termination signal to all workers. It is safe to call Terminate multiple times.
func (ir *InformationRequest) Terminate() {
	ir.Lock()
	defer ir.Unlock()

	if ir.IsTerminated {
		return
	}

	// set the termination signal
	ir.IsTerminated = true
	close(ir.TerminateSignal) // safety guaranteed via lock

	close(ir.ResultChan)
}

// ActiveNodesAdd increases the number of active nodes handling this request
func (ir *InformationRequest) ActiveNodesAdd(count uint64) {
	atomic.AddUint64(&ir.ActiveNodes, count)
}

// ActiveNodesSub decreases the number of active nodes handling this request
func (ir *InformationRequest) ActiveNodesSub(count uint64) {
	if atomic.AddUint64(&ir.ActiveNodes, ^uint64(count-1)) <= 0 {
		// If the counter reaches 0, it means no nodes are handling this request anymore -> terminate it.
		ir.Terminate()
	}
}

// ---- keep track of information requests ----

// irRemove add the information request to the list
// If a request to the same node with the same key exists, it is overwritten. This will be improved. TODO: A nonce could easily fix that?
func (dht *DHT) irAdd(nodeID []byte, request *InformationRequest) {
	dht.listIRmutex.Lock()
	defer dht.listIRmutex.Unlock()

	// The list only supports one request per target node and key. This should be improved.
	lookupKey := string(nodeID) + string(request.Key)
	dht.ListIR[lookupKey] = request

	// auto remove from list upon termination
	go func(lookupKey string, terminateChan <-chan struct{}) {
		<-terminateChan
		dht.irRemove(lookupKey)
	}(lookupKey, request.TerminateSignal)
}

// irRemove removes the information request from the list
func (dht *DHT) irRemove(lookupKey string) {
	dht.listIRmutex.Lock()
	defer dht.listIRmutex.Unlock()

	delete(dht.ListIR, lookupKey)
}

// IRLookup looks up the information request based on the peers public key and hash
func (dht *DHT) IRLookup(nodeID []byte, hash []byte) (request *InformationRequest) {
	dht.listIRmutex.RLock()
	defer dht.listIRmutex.RUnlock()

	lookupKey := string(nodeID) + string(request.Key)
	request = dht.ListIR[lookupKey]

	return request
}
