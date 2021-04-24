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

	return
}

// CollectResults collects all information request responses within the given timeout.
func (ir *InformationRequest) CollectResults(timeout time.Duration) (results []*NodeMessage) {
	for {
		select {
		case result, ok := <-ir.ResultChan:
			if !ok { // channel closed?
				return
			}

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

// Done is called when a remote node is done.
func (ir *InformationRequest) Done() {
	if atomic.AddUint64(&ir.ActiveNodes, ^uint64(0)) <= 0 {
		// If the counter reaches 0, it means no nodes are handling this request anymore -> terminate it.
		ir.Terminate()
	}
}
