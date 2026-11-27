package engine

import "testing"

// Position.Run places a polymetric lane for a row heard, where RunStart is
// as of the last row rendered: after a Rewind, a row from before it still
// reports the run it was in, though RunStart has moved on.
func TestPositionRunIsTheRunOfTheRowAsked(t *testing.T) {
	p, _ := DeriveMood("village", 1, 3, 0)
	s := NewSynth(p)
	playedRMS(s, 2) // the generator first, so the grid's run starts past row 0
	lane := "x..|"
	n := len(lane) - 1
	g := Grid{}
	g.Drums[0] = lane
	s.SetGrid(&g)
	_, cursor, _ := s.NoteEvents(0, nil)
	playedRMS(s, 6)
	before, ok := s.Position()
	if !ok || before.Run <= rows {
		t.Fatalf("after six seconds of a grid, position %+v, ok %v: want a run past its first pattern", before, ok)
	}
	if want := before.Pattern*rows + before.Row - s.RunStart(); before.Run != want {
		t.Errorf("run %d at the last row rendered, want %d by RunStart", before.Run, want)
	}
	s.Rewind()
	playedRMS(s, 1)
	if at, ok := s.PositionAt(before.Frame); !ok || at.Run != before.Run {
		t.Errorf("the row from before the rewind reports run %d (ok %v), want %d still", at.Run, ok, before.Run)
	}
	after, _ := s.Position()
	if after.Run >= before.Run {
		t.Errorf("run %d a second after a rewind, want it counted from the rewind (it was %d before)", after.Run, before.Run)
	}
	evs, _, _ := s.NoteEvents(cursor, nil)
	checked := 0
	for _, e := range evs {
		if e.Layer != layerDrums || e.Lane != 0 || (e.Kind != DrumOn && e.Kind != DrumOff) {
			continue
		}
		at, ok := s.PositionAt(e.Frame)
		if !ok || at.Row != e.Row || at.Pattern != e.Pattern {
			continue // older than the log
		}
		six := 2 * at.Run
		if e.Kind == DrumOff {
			six++
		}
		if c := lane[six%n]; c != 'x' {
			t.Fatalf("a hit at pattern %d row %d (%v) falls %d sixteenths into its run, on %q", e.Pattern, e.Row, e.Kind, six, c)
		}
		checked++
	}
	if checked < 8 {
		t.Errorf("%d hits placed by Position.Run: too few to tell", checked)
	}
}
