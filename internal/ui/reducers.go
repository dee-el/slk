// internal/ui/reducers.go
//
// Phase 4 of the SOLID refactor of internal/ui/app.go: opens the
// monolithic Update switch to extension via a chain of cohesive
// per-feature reducers.
//
// Each reducer owns one message family (presence, image preview,
// channel, send, threads, ...) and either handles the message
// (returns cmd, true) or passes (returns nil, false). App.Update
// tries reducers in registration order via dispatchReducers and
// stops at the first true return; if no reducer claims the message,
// the residual switch in Update picks it up.
//
// Migration strategy: arms come out of the switch one family at a
// time. Each phase moves a cohesive group onto its owning controller
// (when one exists) or into a new reducer_*.go file (when no owner
// exists yet). The switch in Update shrinks as each lands.
//
// Why a typed interface and not a reflect-based table:
//   - Compile-time obvious: every reducer is a concrete type and the
//     dispatch chain is a plain []reducer literal in Update.
//   - Zero dispatch overhead.
//   - Mirrors the controller/service pattern from Phases 2-3: a
//     reducer is just another collaborator with a narrow interface.
package ui

import (
	tea "charm.land/bubbletea/v2"
)

// reducer handles a cohesive subset of the message types fed into
// App.Update. Implementations perform a typed switch on msg and:
//   - mutate App state and return (cmd, true) when they own the
//     message type; cmd may be nil if no follow-up command is needed.
//   - return (nil, false) when the message is not their
//     responsibility, so the next reducer (or the residual switch)
//     can attempt it.
//
// Reducers must not chain to other reducers internally — dispatch
// ordering is the caller's responsibility (see dispatchReducers).
type reducer interface {
	Handle(a *App, msg tea.Msg) (tea.Cmd, bool)
}

// reducerFunc adapts a plain function with the reducer signature
// into the reducer interface. Used by the free reducer files
// (reducer_reactions.go, reducer_threads.go, ...) so they don't
// need a wrapper struct just to satisfy the interface — the
// per-file `var reduceXxx reducerFunc = func(...) {...}` literal
// reads as a single unit.
//
// Mirrors net/http's HandlerFunc adapter.
type reducerFunc func(a *App, msg tea.Msg) (tea.Cmd, bool)

// Handle satisfies the reducer interface.
func (f reducerFunc) Handle(a *App, msg tea.Msg) (tea.Cmd, bool) {
	return f(a, msg)
}

// dispatchReducers tries each reducer in order; returns (cmd, true)
// at the first handler that owns msg, or (nil, false) when no
// reducer claimed it.
//
// Variadic so Update can declare the chain inline as a literal,
// keeping the dispatch order visible at the call site:
//
//	if cmd, handled := dispatchReducers(a, msg,
//	    a.presence,
//	    a.preview,
//	    ...,
//	); handled { ... }
func dispatchReducers(a *App, msg tea.Msg, rs ...reducer) (tea.Cmd, bool) {
	for _, r := range rs {
		if cmd, ok := r.Handle(a, msg); ok {
			return cmd, true
		}
	}
	return nil, false
}
