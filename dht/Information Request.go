/*
File Name:  Information Request.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Information requests are asynchronous queries sent to nodes.
*/

package dht

import "time"

// InformationRequest is an asynchronous request sent. It tracks any asynchronous replies and handles timeouts.
type InformationRequest struct {
	Action int    // IterateFindNode or IterateFindValue
	Key    []byte // Target key

	// TODO: Include results channel? Timeout settings? Cancelation?
}

type message2 struct {
	SenderID []byte // Sender of this message
	Data     []byte
	Closest  []*Node
	Error    error
}

// infoCollectResults collects all information request responses within the given timeout.
func infoCollectResults(resultChan chan *message2, timeout time.Duration) (results []*message2) {
	for {
		select {
		case result, ok := <-resultChan:
			if !ok {
				return
			}
			results = append(results, result)
		case <-time.After(timeout):
			// send cancelation signal ?
			//close(resultChan)
			return
		}
	}
}
