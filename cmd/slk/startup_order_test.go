package main

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestStartWorkspaceRealtimeSendsReadyBeforeRunningConn(t *testing.T) {
	readyEntered := make(chan struct{}, 1)
	releaseReady := make(chan struct{})
	connRan := make(chan bool, 1)
	var readyComplete atomic.Bool

	go startWorkspaceRealtime(
		func() {
			readyEntered <- struct{}{}
			<-releaseReady
			readyComplete.Store(true)
		},
		func() {
			connRan <- readyComplete.Load()
		},
	)

	select {
	case <-readyEntered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ready send to start")
	}

	select {
	case ranBeforeReady := <-connRan:
		t.Fatalf("connection ran before ready send finished: %v", ranBeforeReady)
	default:
	}

	close(releaseReady)

	select {
	case ranAfterReady := <-connRan:
		if !ranAfterReady {
			t.Fatal("connection ran before ready send completed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for connection start")
	}
}
