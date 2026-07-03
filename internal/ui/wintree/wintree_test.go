package wintree

import (
	"reflect"
	"testing"
)

func TestNew_SingleLeaf(t *testing.T) {
	tr, id := New(Channel{ID: "C1", Name: "general", Type: "channel"})
	if got := tr.Leaves(); !reflect.DeepEqual(got, []LeafID{id}) {
		t.Fatalf("Leaves() = %v, want [%v]", got, id)
	}
	if tr.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", tr.Len())
	}
	ch, ok := tr.Channel(id)
	if !ok || ch.Name != "general" {
		t.Fatalf("Channel(%v) = %+v, %v", id, ch, ok)
	}
}

func TestSetChannel(t *testing.T) {
	tr, id := New(Channel{ID: "C1", Name: "general"})
	if !tr.SetChannel(id, Channel{ID: "C2", Name: "ops"}) {
		t.Fatal("SetChannel returned false for existing leaf")
	}
	if ch, _ := tr.Channel(id); ch.ID != "C2" {
		t.Fatalf("channel = %+v, want C2", ch)
	}
	if tr.SetChannel(LeafID(999), Channel{}) {
		t.Fatal("SetChannel should return false for unknown leaf")
	}
}

func TestComputeRects_SingleWindowFillsBounds(t *testing.T) {
	tr, id := New(Channel{})
	bounds := Rect{X: 0, Y: 0, W: 120, H: 40}
	rects := tr.ComputeRects(bounds)
	if rects[id] != bounds {
		t.Fatalf("rect = %+v, want %+v", rects[id], bounds)
	}
}
