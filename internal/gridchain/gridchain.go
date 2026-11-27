// Package gridchain keeps the grid example's names for the grids of a chain,
// and decides how a new list of them reaches the engine. It is apart from
// the example because that builds only for the browser, where nothing tests
// it; this has no engine or js in it and is tested natively.
//
// The page names each grid of a chain by its slot number. The engine knows a
// theme's grids by their place in the list, and keeps its own place by that
// number, so a list with other grids before the one playing cannot land
// mid-pattern: the number would name another grid. Names says when a list
// may land, and what the engine's number meant at a pattern already played.
package gridchain

import "slices"

// Step is how a list of grids is handed to the engine.
type Step int

const (
	// Restart plays the list from its first grid.
	Restart Step = iota
	// AtNextGrid lands the list as the next grid starts.
	AtNextGrid
	// AtNextRow lands it at the next row, keeping the place.
	AtNextRow
)

// Names is the chain's names. The zero value is no chain.
type Names struct {
	on bool
	// ids names the grids playing; while moving, next names those that take
	// over once pattern after has played
	ids, next []int
	after     int
	moving    bool
	// was names the grids of the patterns up to until, once a staged list
	// has taken over: the last of them may be rendered and not yet heard
	was   []int
	until int
	held  bool
}

// On reports whether a chain's grids are the ones playing.
func (n *Names) On() bool { return n.on }

// Clear ends the chain, and reports whether there was one to end.
func (n *Names) Clear() bool {
	was := n.on
	n.on = false
	return was
}

// Set takes the names of a new list and returns how it should reach the
// engine. pattern is the last pattern rendered, themePlaying whether the
// engine is still playing a theme, and sounding whether anyone hears it.
func (n *Names) Set(ids []int, pattern int, themePlaying, sounding bool) Step {
	// the names as of the last pattern rendered: a change staged for a grid
	// that has since started is in effect
	if n.moving && pattern > n.after {
		n.was, n.until, n.held = n.ids, n.after, true
		n.ids, n.moving = n.next, false
	}
	moved := !slices.Equal(ids, n.ids) || n.moving && !slices.Equal(ids, n.next)
	was := n.on
	n.on = true
	switch {
	case !was || !themePlaying || moved && !sounding:
		// stopped, nobody hears the place lost
		n.ids, n.moving, n.held = ids, false, false
		return Restart
	case moved || n.moving:
		// The engine keeps its place by number, so with other grids before
		// it the number would name another grid mid-pattern. It lands as the
		// next grid starts instead, and so does any edit until then, which
		// would otherwise land the new list at once.
		n.next, n.after, n.moving = ids, pattern, true
		return AtNextGrid
	default:
		// an edit to one grid must not send the listener back to the first
		return AtNextRow
	}
}

// At is the name of the grid numbered grid as of pattern, or -1 where it has
// none. pattern may be behind the last one given to Set, by what is rendered
// and not yet heard.
func (n *Names) At(pattern, grid int) int {
	ids := n.ids
	switch {
	case n.held && pattern <= n.until:
		ids = n.was
	case n.moving && pattern > n.after:
		ids = n.next
	}
	if grid < 0 || grid >= len(ids) {
		return -1
	}
	return ids[grid]
}
