package wintree

// LayoutNode is the renderable shape of the tree: leaves carry their
// window id and rect; splits carry direction and children. The UI
// walks this to compose window panes (it never sees *node).
type LayoutNode struct {
	Leaf     bool
	ID       LeafID
	Rect     Rect
	Dir      Dir
	Children []LayoutNode
}

// Layout resolves every node's rect within bounds. Children of a
// split divide the parent extent equally; remainders go to the
// earliest children one cell each, so rects always tile exactly.
func (t *Tree) Layout(bounds Rect) LayoutNode {
	return layoutNode(t.root, bounds)
}

func layoutNode(n *node, r Rect) LayoutNode {
	if n.isLeaf() {
		return LayoutNode{Leaf: true, ID: n.id, Rect: r}
	}
	out := LayoutNode{Rect: r, Dir: n.dir, Children: make([]LayoutNode, 0, len(n.children))}
	k := len(n.children)
	if n.dir == SplitSideBySide {
		base, rem := r.W/k, r.W%k
		x := r.X
		for i, c := range n.children {
			w := base
			if i < rem {
				w++
			}
			out.Children = append(out.Children, layoutNode(c, Rect{X: x, Y: r.Y, W: w, H: r.H}))
			x += w
		}
	} else {
		base, rem := r.H/k, r.H%k
		y := r.Y
		for i, c := range n.children {
			h := base
			if i < rem {
				h++
			}
			out.Children = append(out.Children, layoutNode(c, Rect{X: r.X, Y: y, W: r.W, H: h}))
			y += h
		}
	}
	return out
}

// ComputeRects flattens Layout into a per-window rect map.
func (t *Tree) ComputeRects(bounds Rect) map[LeafID]Rect {
	out := make(map[LeafID]Rect)
	var walk func(LayoutNode)
	walk = func(n LayoutNode) {
		if n.Leaf {
			out[n.ID] = n.Rect
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(t.Layout(bounds))
	return out
}

// nodeRect returns the rect of an internal *node within bounds
// (used by Split's refusal check). Same division as Layout.
func (t *Tree) nodeRect(target *node, bounds Rect) (Rect, bool) {
	var found Rect
	var ok bool
	var walk func(n *node, r Rect)
	walk = func(n *node, r Rect) {
		if n == target {
			found, ok = r, true
			return
		}
		if n.isLeaf() {
			return
		}
		k := len(n.children)
		if n.dir == SplitSideBySide {
			base, rem := r.W/k, r.W%k
			x := r.X
			for i, c := range n.children {
				w := base
				if i < rem {
					w++
				}
				walk(c, Rect{X: x, Y: r.Y, W: w, H: r.H})
				x += w
			}
		} else {
			base, rem := r.H/k, r.H%k
			y := r.Y
			for i, c := range n.children {
				h := base
				if i < rem {
					h++
				}
				walk(c, Rect{X: r.X, Y: y, W: r.W, H: h})
				y += h
			}
		}
	}
	walk(t.root, bounds)
	return found, ok
}
