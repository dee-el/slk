// internal/ui/services_helpers_test.go
//
// Test-only helper methods on App that wire single ThreadService
// closures. The production surface (SetThreadService) takes a full
// ThreadServiceFuncs bundle; most tests only need one closure, and
// these helpers preserve the original SetThreadFetcher / SetThreadsListFetcher
// call style without polluting the production API.
//
// File name ends in _test.go so these are invisible outside the test
// binary.
package ui

func (a *App) setThreadFetcherForTest(fn ThreadFetchFunc) {
	a.SetThreadService(NewThreadService(ThreadServiceFuncs{Fetch: fn}))
}

func (a *App) setThreadsListFetcherForTest(fn ThreadsListFetchFunc) {
	a.SetThreadService(NewThreadService(ThreadServiceFuncs{ListFetch: fn}))
}

func (a *App) setPermalinkFetcherForTest(fn PermalinkFetchFunc) {
	a.SetMessageService(NewMessageService(MessageServiceFuncs{Permalink: fn}))
}
