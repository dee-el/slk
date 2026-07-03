// Package wintree owns the vim-style window split tree for the
// messages region (window-management design §1). Pure data + geometry:
// no UI dependencies. Internal nodes are splits (direction + children,
// always divided equally in Phase 2); leaves are windows identified by
// a stable LeafID and carrying the channel they view.
package wintree

import "errors"

// Dir is a split direction, named by visual result to avoid vim's
// confusing horizontal/vertical terminology.
type Dir int

const (
	// SplitStacked is vim's :sp — children stack top-to-bottom.
	SplitStacked Dir = iota
	// SplitSideBySide is vim's :vsp — children sit left-to-right.
	SplitSideBySide
)

// NavDir is a geometric focus-navigation direction (ctrl+w h/j/k/l).
type NavDir int

const (
	NavLeft NavDir = iota
	NavDown
	NavUp
	NavRight
)

// LeafID identifies a window. IDs are stable for the window's
// lifetime and never reused within a Tree.
type LeafID int

// Channel is the channel a window views. Mirrors the fields of
// ui.ChannelSelectedMsg so focus changes can re-dispatch selection.
type Channel struct {
	ID   string
	Name string
	Type string
}

// Rect is a window rectangle in screen cells, including the window's
// border rows/cols. Rects produced by ComputeRects tile the bounds
// exactly.
type Rect struct {
	X, Y, W, H int
}

// Minimum window rect sizes, border inclusive. MinWidth matches the
// messages pane's 40-col content minimum plus its 2 border cols.
const (
	MinWidth  = 42
	MinHeight = 8
)

var (
	ErrNotFound   = errors.New("wintree: no such window")
	ErrNoRoom     = errors.New("wintree: not enough room")
	ErrLastWindow = errors.New("wintree: cannot close last window")
)

// node is either a leaf (len(children) == 0; id/ch valid) or a split
// (dir/children valid).
type node struct {
	id       LeafID
	ch       Channel
	dir      Dir
	children []*node
}

func (n *node) isLeaf() bool { return len(n.children) == 0 }

// Tree is the window tree. Zero value is not usable; construct with New.
type Tree struct {
	root *node
	next LeafID
}

// New returns a tree with a single window viewing ch, and that
// window's id.
func New(ch Channel) (*Tree, LeafID) {
	t := &Tree{next: 2}
	t.root = &node{id: 1, ch: ch}
	return t, 1
}

// Len returns the number of windows.
func (t *Tree) Len() int { return len(t.Leaves()) }

// Leaves returns all window ids in tree (depth-first, left-to-right /
// top-to-bottom) order.
func (t *Tree) Leaves() []LeafID {
	var out []LeafID
	var walk func(n *node)
	walk = func(n *node) {
		if n.isLeaf() {
			out = append(out, n.id)
			return
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(t.root)
	return out
}

// findLeaf returns the leaf with the given id and its parent split
// (parent == nil when the leaf is the root). nil leaf means not found.
func (t *Tree) findLeaf(id LeafID) (leaf, parent *node) {
	var walk func(n, p *node) (*node, *node)
	walk = func(n, p *node) (*node, *node) {
		if n.isLeaf() {
			if n.id == id {
				return n, p
			}
			return nil, nil
		}
		for _, c := range n.children {
			if l, lp := walk(c, n); l != nil {
				return l, lp
			}
		}
		return nil, nil
	}
	return walk(t.root, nil)
}

// Channel returns the channel of the given window.
func (t *Tree) Channel(id LeafID) (Channel, bool) {
	l, _ := t.findLeaf(id)
	if l == nil {
		return Channel{}, false
	}
	return l.ch, true
}

// SetChannel updates the channel of the given window. Returns false
// if the window does not exist.
func (t *Tree) SetChannel(id LeafID, ch Channel) bool {
	l, _ := t.findLeaf(id)
	if l == nil {
		return false
	}
	l.ch = ch
	return true
}

// firstLeaf returns the first (tree-order) leaf under n.
func firstLeaf(n *node) *node {
	for !n.isLeaf() {
		n = n.children[0]
	}
	return n
}
