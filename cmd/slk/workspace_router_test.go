package main

import (
	"fmt"
	"sync"
	"testing"
)

func TestWorkspaceRouterSnapshotImmutableAndConcurrentAccess(t *testing.T) {
	router := newWorkspaceRouter()
	router.Add("T0", &WorkspaceContext{TeamID: "T0"})

	snap := router.Snapshot()
	delete(snap, "T0")
	if got := router.ByID("T0"); got == nil || got.TeamID != "T0" {
		t.Fatalf("router lost T0 after snapshot mutation: %+v", got)
	}

	const workers = 8
	const iterations = 200

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				teamID := fmt.Sprintf("T%d", (worker*iterations+j)%32)
				wctx := &WorkspaceContext{TeamID: teamID}
				router.Add(teamID, wctx)
				router.Set(wctx)
				_ = router.ByID(teamID)
				_ = router.Active()
				_ = router.Snapshot()
			}
		}(i)
	}
	wg.Wait()

	snap = router.Snapshot()
	if len(snap) == 0 {
		t.Fatal("router snapshot empty after concurrent writes")
	}
	for teamID, wctx := range snap {
		if wctx == nil || wctx.TeamID != teamID {
			t.Fatalf("snapshot[%q] = %+v", teamID, wctx)
		}
	}
}
