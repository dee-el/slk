// cmd/slk/search_workspace_test.go
//
// Tests for the SearchWorkspace service closure wiring.
package main

import (
	"testing"

	"github.com/gammons/slk/internal/ui"
)

// TestSearchWorkspaceNoActiveWorkspaceReturnsErr verifies the
// SearchWorkspace closure never returns a nil msg when no workspace
// is active: a nil msg would leave the ctrl+f modal spinner stuck
// (the reducer only clears loading on a WorkspaceSearchResultsMsg).
func TestSearchWorkspaceNoActiveWorkspaceReturnsErr(t *testing.T) {
	fn := searchWorkspaceFunc(newWorkspaceRouter(), nil, "15:04")
	msg := fn("deploy")
	res, ok := msg.(ui.WorkspaceSearchResultsMsg)
	if !ok {
		t.Fatalf("got %T, want ui.WorkspaceSearchResultsMsg", msg)
	}
	if res.Query != "deploy" || res.Err == nil {
		t.Fatalf("res = %+v, want Query=%q and non-nil Err", res, "deploy")
	}
}
