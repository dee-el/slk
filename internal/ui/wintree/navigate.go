package wintree

// NavigateDir returns the window adjacent to id in the given
// direction (vim ctrl+w h/j/k/l): among windows whose rect touches
// id's rect edge-on in that direction WITH positive perpendicular
// overlap (corner-only contact does not count), the one with the
// largest overlap wins. Ties resolve to the earliest window in tree
// order (deterministic). ok=false when there is no neighbor.
func (t *Tree) NavigateDir(id LeafID, nd NavDir, bounds Rect) (LeafID, bool) {
	rects := t.ComputeRects(bounds)
	cur, ok := rects[id]
	if !ok {
		return id, false
	}
	best := id
	bestOverlap := 0                 // require > 0: corner contact is not adjacency
	for _, lid := range t.Leaves() { // tree order => deterministic ties
		if lid == id {
			continue
		}
		r := rects[lid]
		var adjacent bool
		var overlap int
		switch nd {
		case NavLeft:
			adjacent = r.X+r.W == cur.X
			overlap = overlap1D(r.Y, r.H, cur.Y, cur.H)
		case NavRight:
			adjacent = cur.X+cur.W == r.X
			overlap = overlap1D(r.Y, r.H, cur.Y, cur.H)
		case NavUp:
			adjacent = r.Y+r.H == cur.Y
			overlap = overlap1D(r.X, r.W, cur.X, cur.W)
		case NavDown:
			adjacent = cur.Y+cur.H == r.Y
			overlap = overlap1D(r.X, r.W, cur.X, cur.W)
		}
		if adjacent && overlap > bestOverlap {
			best, bestOverlap = lid, overlap
		}
	}
	if best == id {
		return id, false
	}
	return best, true
}

// overlap1D returns the overlap length of [a, a+alen) and [b, b+blen),
// negative when disjoint.
func overlap1D(a, alen, b, blen int) int {
	lo := max(a, b)
	hi := min(a+alen, b+blen)
	return hi - lo
}
