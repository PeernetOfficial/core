/*
File Name:  Information Request.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Information requests are asynchronous queries sent to nodes.
*/

package dht

import (
	"sync"
	"time"
)

// InformationRequest is an asynchronous request sent. It tracks any asynchronous replies and handles timeouts.
type InformationRequest struct {
	Action          int               // IterateFindNode or IterateFindValue
	Key             []byte            // Target key
	ResultChan      chan *NodeMessage // Result channel
	IsTerminated    bool              // If true, it was signaled for termination
	TerminateSignal chan interface{}  // gets closed on termination signal, can be used in select via "case _ = <- network.terminateSignal:"
	sync.Mutex                        // for sychronized closing
}

// NewInformationRequest creates a new information request
func NewInformationRequest(Action int, Key []byte) (ir *InformationRequest) {
	return &InformationRequest{
		ResultChan: make(chan *NodeMessage),
		Action:     Action,
		Key:        Key,
	}
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

// TODO: Incoming information request responses should be handled in batches, i.e. every X ms (for example 100ms) without waiting for all results to finish.
// This could seriously speed up discovery.
