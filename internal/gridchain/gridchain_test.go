package gridchain

import "testing"

func TestFirstListStartsFromItsFirstGrid(t *testing.T) {
	var n Names
	if n.On() {
		t.Fatal("the zero value is a chain")
	}
	if got := n.Set([]int{0, 1}, 3, false, true); got != Restart {
		t.Fatalf("first list: step %v, want Restart", got)
	}
	if !n.On() {
		t.Fatal("not on after Set")
	}
	if a, b := n.At(3, 0), n.At(3, 1); a != 0 || b != 1 {
		t.Fatalf("names %d %d, want 0 1", a, b)
	}
}

func TestAnEditKeepsThePlace(t *testing.T) {
	var n Names
	n.Set([]int{0, 1}, 0, false, true)
	// the same names: one grid's notes changed
	if got := n.Set([]int{0, 1}, 4, true, true); got != AtNextRow {
		t.Fatalf("an edit: step %v, want AtNextRow", got)
	}
}

func TestNewNamesLandAtTheNextGrid(t *testing.T) {
	var n Names
	n.Set([]int{0, 1}, 0, false, true)
	// slot 2 put before the grid playing: the engine's number 1 would name it mid-pattern
	if got := n.Set([]int{2, 0, 1}, 5, true, true); got != AtNextGrid {
		t.Fatalf("new names: step %v, want AtNextGrid", got)
	}
	// pattern 5 is still the old list's, as rendered and as heard
	if got := n.At(5, 1); got != 1 {
		t.Fatalf("grid 1 of pattern 5 is %d, want 1", got)
	}
	// from pattern 6 the new list's
	if got := n.At(6, 1); got != 0 {
		t.Fatalf("grid 1 of pattern 6 is %d, want 0", got)
	}
	if got := n.At(6, 2); got != 1 {
		t.Fatalf("grid 2 of pattern 6 is %d, want 1", got)
	}
}

func TestAnEditWhileNamesAreStagedWaitsWithThem(t *testing.T) {
	var n Names
	n.Set([]int{0, 1}, 0, false, true)
	n.Set([]int{2, 0, 1}, 5, true, true)
	// The staged names again, with a note changed, in the same pattern: at
	// the next row it would land the three-grid list mid-pattern.
	if got := n.Set([]int{2, 0, 1}, 5, true, true); got != AtNextGrid {
		t.Fatalf("an edit while staged: step %v, want AtNextGrid", got)
	}
	// once the staged list has started, the same edit keeps the place
	if got := n.Set([]int{2, 0, 1}, 6, true, true); got != AtNextRow {
		t.Fatalf("an edit after the names landed: step %v, want AtNextRow", got)
	}
	if got := n.At(6, 0); got != 2 {
		t.Fatalf("grid 0 of pattern 6 is %d, want 2", got)
	}
}

func TestGoingBackWhileStagedIsStillAChange(t *testing.T) {
	var n Names
	n.Set([]int{0, 1}, 0, false, true)
	n.Set([]int{0, 1, 2}, 5, true, true)
	// back to the names playing, before the staged ones land: the engine
	// holds the staged list, so this has to replace it
	if got := n.Set([]int{0, 1}, 5, true, true); got != AtNextGrid {
		t.Fatalf("back to the first names: step %v, want AtNextGrid", got)
	}
	if got := n.At(6, 2); got != -1 {
		t.Fatalf("grid 2 of pattern 6 is %d, want none", got)
	}
}

func TestStoppedNewNamesStartOver(t *testing.T) {
	var n Names
	n.Set([]int{0, 1}, 0, false, true)
	// nobody hears the place lost
	if got := n.Set([]int{1, 0}, 2, true, false); got != Restart {
		t.Fatalf("new names, stopped: step %v, want Restart", got)
	}
	if got := n.At(2, 0); got != 1 {
		t.Fatalf("grid 0 is %d, want 1", got)
	}
	// stopped, the same names are an edit like any other
	if got := n.Set([]int{1, 0}, 2, true, false); got != AtNextRow {
		t.Fatalf("an edit, stopped: step %v, want AtNextRow", got)
	}
}

func TestAThemeThatEndedStartsOver(t *testing.T) {
	var n Names
	n.Set([]int{0, 1}, 0, false, true)
	// something else took the engine: there is no place to keep
	if got := n.Set([]int{0, 1}, 4, false, true); got != Restart {
		t.Fatalf("theme not playing: step %v, want Restart", got)
	}
}

func TestClear(t *testing.T) {
	var n Names
	if n.Clear() {
		t.Fatal("cleared a chain that was not on")
	}
	n.Set([]int{0, 1}, 0, false, true)
	if !n.Clear() || n.On() {
		t.Fatal("Clear did not end the chain")
	}
	// the chain again: from its first grid, though the theme may still be the engine's
	if got := n.Set([]int{0, 1}, 3, true, true); got != Restart {
		t.Fatalf("after Clear: step %v, want Restart", got)
	}
}

func TestAtOutOfRange(t *testing.T) {
	var n Names
	if got := n.At(0, 0); got != -1 {
		t.Fatalf("no chain: %d, want -1", got)
	}
	n.Set([]int{4, 7}, 0, false, true)
	for _, g := range []int{-1, 2, 9} {
		if got := n.At(0, g); got != -1 {
			t.Fatalf("grid %d: %d, want -1", g, got)
		}
	}
}
