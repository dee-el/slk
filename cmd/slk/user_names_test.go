package main

import (
	"fmt"
	"sync"
	"testing"
)

func TestUserNameStoreSnapshotImmutable(t *testing.T) {
	store := newUserNameStore(map[string]string{"U1": "alice"})

	snapshot := store.Snapshot()
	snapshot["U1"] = "changed"
	snapshot["U2"] = "bob"

	if got, _ := store.Get("U1"); got != "alice" {
		t.Fatalf("store U1 = %q, want alice", got)
	}
	if _, ok := store.Get("U2"); ok {
		t.Fatal("snapshot mutation leaked into store")
	}
}

func TestUserNameStoreMerge(t *testing.T) {
	store := newUserNameStore(map[string]string{"U1": "alice"})
	store.Merge(map[string]string{"U1": "Alice", "U2": "bob"})

	got := store.Snapshot()
	if got["U1"] != "Alice" {
		t.Fatalf("U1 = %q, want Alice", got["U1"])
	}
	if got["U2"] != "bob" {
		t.Fatalf("U2 = %q, want bob", got["U2"])
	}
}

func TestUserNameStoreConcurrentAccess(t *testing.T) {
	store := newUserNameStore(nil)

	const workers = 8
	const iterations = 200

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				id := fmt.Sprintf("U%d", j%16)
				store.Set(id, fmt.Sprintf("name-%d-%d", worker, j))
				store.Get(id)
				_ = store.Snapshot()
			}
		}(i)
	}
	wg.Wait()

	if len(store.Snapshot()) == 0 {
		t.Fatal("store stayed empty after concurrent writes")
	}
}

func TestHandleNameStoreSnapshotImmutable(t *testing.T) {
	store := newHandleNameStore(map[string]string{"alice": "Alice"})

	snapshot := store.Snapshot()
	snapshot["alice"] = "changed"
	snapshot["bob"] = "Bob"

	if got, _ := store.Get("alice"); got != "Alice" {
		t.Fatalf("store alice = %q, want Alice", got)
	}
	if _, ok := store.Get("bob"); ok {
		t.Fatal("snapshot mutation leaked into handle store")
	}
}

func TestHandleNameStoreConcurrentAccess(t *testing.T) {
	store := newHandleNameStore(nil)

	const workers = 8
	const iterations = 200

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				handle := fmt.Sprintf("user-%d", j%16)
				store.Set(handle, fmt.Sprintf("name-%d-%d", worker, j))
				store.Get(handle)
				_ = store.Snapshot()
			}
		}(i)
	}
	wg.Wait()

	if len(store.Snapshot()) == 0 {
		t.Fatal("handle store stayed empty after concurrent writes")
	}
}

func TestBotUserIDStoreSnapshotImmutable(t *testing.T) {
	store := newBotUserIDStore(map[string]bool{"U1": true})

	snapshot := store.Snapshot()
	delete(snapshot, "U1")
	snapshot["U2"] = true

	if !store.Has("U1") {
		t.Fatal("snapshot mutation removed stored bot id")
	}
	if store.Has("U2") {
		t.Fatal("snapshot mutation leaked into bot store")
	}
}

func TestBotUserIDStoreConcurrentAccess(t *testing.T) {
	store := newBotUserIDStore(nil)

	const workers = 8
	const iterations = 200

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				id := fmt.Sprintf("U%d", (worker*iterations+j)%32)
				store.Set(id)
				store.Has(id)
				_ = store.Snapshot()
			}
		}(i)
	}
	wg.Wait()

	if len(store.Snapshot()) == 0 {
		t.Fatal("bot store stayed empty after concurrent writes")
	}
}
