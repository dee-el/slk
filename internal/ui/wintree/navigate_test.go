package wintree

import "testing"

// grid3 builds: left column a (full height) | right column b over c.
func grid3(t *testing.T) (*Tree, LeafID, LeafID, LeafID, Rect) {
	t.Helper()
	bounds := Rect{X: 0, Y: 0, W: 180, H: 48}
	tr, a := New(Channel{})
	b, err := tr.Split(a, SplitSideBySide, bounds)
	if err != nil {
		t.Fatal(err)
	}
	c, err := tr.Split(b, SplitStacked, bounds)
	if err != nil {
		t.Fatal(err)
	}
	return tr, a, b, c, bounds
}

func TestNavigateDir_LeftRight(t *testing.T) {
	tr, a, b, c, bounds := grid3(t)
	if got, ok := tr.NavigateDir(a, NavRight, bounds); !ok || got != b {
		t.Fatalf("a right = %v %v, want %v (largest overlap: b is upper)", got, ok, b)
	}
	if got, ok := tr.NavigateDir(b, NavLeft, bounds); !ok || got != a {
		t.Fatalf("b left = %v %v, want %v", got, ok, a)
	}
	if got, ok := tr.NavigateDir(c, NavLeft, bounds); !ok || got != a {
		t.Fatalf("c left = %v %v, want %v", got, ok, a)
	}
}

func TestNavigateDir_UpDown(t *testing.T) {
	tr, _, b, c, bounds := grid3(t)
	if got, ok := tr.NavigateDir(b, NavDown, bounds); !ok || got != c {
		t.Fatalf("b down = %v %v, want %v", got, ok, c)
	}
	if got, ok := tr.NavigateDir(c, NavUp, bounds); !ok || got != b {
		t.Fatalf("c up = %v %v, want %v", got, ok, b)
	}
}

func TestNavigateDir_NoNeighbor(t *testing.T) {
	tr, a, _, _, bounds := grid3(t)
	if got, ok := tr.NavigateDir(a, NavLeft, bounds); ok || got != a {
		t.Fatalf("a left should report no neighbor, got %v %v", got, ok)
	}
	if _, ok := tr.NavigateDir(a, NavUp, bounds); ok {
		t.Fatal("a up should report no neighbor")
	}
}

func TestNavigateDir_PicksLargestOverlap(t *testing.T) {
	// left column split into two rows (a top, d bottom); right column
	// b over c. From b going left, a (top row) overlaps b's Y-range
	// more than d does.
	bounds := Rect{X: 0, Y: 0, W: 180, H: 48}
	tr, a := New(Channel{})
	b, _ := tr.Split(a, SplitSideBySide, bounds)
	c, _ := tr.Split(b, SplitStacked, bounds)
	d, _ := tr.Split(a, SplitStacked, bounds)
	_ = c
	if got, ok := tr.NavigateDir(b, NavLeft, bounds); !ok || got != a {
		t.Fatalf("b left = %v, want %v (top-left window)", got, a)
	}
	if got, ok := tr.NavigateDir(c, NavLeft, bounds); !ok || got != d {
		t.Fatalf("c left = %v, want %v (bottom-left window)", got, d)
	}
}
