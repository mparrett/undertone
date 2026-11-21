package engine

import (
	"fmt"
	"slices"
	"testing"
)

// key is an event without its frame, for comparing against the drawn
// patterns.
type noteKey struct {
	layer, lane, note, row, pat int
	gain                        float32
}

func events(s *Synth) []NoteEvent {
	evs, _, _ := s.NoteEvents(0, nil)
	return evs
}

// Every note the drawn patterns hold over two patterns is reported once,
// on its row and at the frame that row started.
func TestNoteEventsMatchThePatterns(t *testing.T) {
	p := bare(DeriveDungeon(1, 2, 0.6))
	s := NewSynth(p)
	render(s, 2*rows*s.samplesPerRow)
	var want []noteKey
	for pat := range 2 {
		drawn := buildPatterns(s.params, pat)
		for tr := range nTracks {
			for row, n := range drawn[tr] {
				if n.note != 0 {
					want = append(want, noteKey{tr, 0, clampNote(n.note), row, pat, float32(n.gain)})
				}
			}
		}
	}
	var got []noteKey
	for _, e := range events(s) {
		if e.Layer == layerDrums {
			continue
		}
		if e.Kind != NoteOn && e.Kind != NoteRetrigger {
			t.Errorf("unexpected %v event %+v with no mid-pattern change", e.Kind, e)
		}
		got = append(got, noteKey{e.Layer, e.Lane, e.Note, e.Row, e.Pattern, float32(e.Gain)})
		if pos, ok := s.PositionAt(e.Frame); !ok || pos.Frame != e.Frame || pos.Row != e.Row || pos.Pattern != e.Pattern {
			t.Errorf("event %+v is not at its row's start (%+v, %v)", e, pos, ok)
		}
	}
	less := func(a, b noteKey) int { return cmpKey(a) - cmpKey(b) }
	slices.SortFunc(want, less)
	slices.SortFunc(got, less)
	if len(want) == 0 || !slices.Equal(got, want) {
		t.Errorf("events:\n got %v\nwant %v", got, want)
	}
}

func cmpKey(k noteKey) int { return ((k.pat*rows+k.row)*NumLayers+k.layer)*128 + k.note }

// Drum strikes are reported per lane: an on 16th at the row's start for
// every hit drawn, off 16ths and ratchets within their row, and graces just
// before the row they lead into.
func TestNoteEventsReportDrumStrikes(t *testing.T) {
	p, _ := DeriveMood("forge", 7, 4, 1)
	s := NewSynth(p)
	render(s, 2*rows*s.samplesPerRow)
	kinds := map[NoteKind]int{}
	for _, e := range events(s) {
		if e.Layer != layerDrums || e.Pattern > 1 {
			continue
		}
		kinds[e.Kind]++
		start := s.rowStart(t, e.Row, e.Pattern)
		h := buildDrums(s.params, e.Pattern)[e.Lane][e.Row]
		switch e.Kind {
		case DrumOn:
			if e.Frame != start || float32(e.Gain) != float32(h.on) {
				t.Errorf("on 16th %+v: want frame %d gain %g", e, start, h.on)
			}
		case DrumOff, DrumRatchet:
			if e.Frame <= start || e.Frame >= start+int64(s.samplesPerRow) {
				t.Errorf("%v %+v outside its row, which starts at %d", e.Kind, e, start)
			}
		case DrumGrace:
			if e.Frame >= start+int64(s.samplesPerRow) || e.Frame < start-int64(s.flamLead())-1 {
				t.Errorf("grace %+v not within a flam's lead of row start %d", e, start)
			}
		default:
			t.Errorf("drum event of kind %v: %+v", e.Kind, e)
		}
	}
	if kinds[DrumOn] == 0 || kinds[DrumOff] == 0 {
		t.Errorf("forge at tension 1 reported %v, want on and off 16ths", kinds)
	}
}

// rowStart is the frame row of pat started on, from the row log.
func (s *Synth) rowStart(t *testing.T, row, pat int) int64 {
	t.Helper()
	for f := s.Frames() - 1; f >= 0; f -= int64(s.samplesPerRow) / 2 {
		if p, ok := s.PositionAt(f); ok && p.Row == row && p.Pattern == pat {
			return p.Frame
		}
	}
	t.Fatalf("no row %d of pattern %d in the log", row, pat)
	return 0
}

// A muted layer plays on unheard, so its notes are still reported.
func TestNoteEventsIncludeMutedLayers(t *testing.T) {
	p := DeriveDungeon(1, 2, 0.6)
	a := NewSynth(p)
	p.Mute = LayerLead | LayerDrone
	b := NewSynth(p)
	render(a, rows*a.samplesPerRow)
	render(b, rows*b.samplesPerRow)
	if ea, eb := events(a), events(b); !slices.Equal(ea, eb) || len(ea) == 0 {
		t.Errorf("muting changed the events: %d unmuted, %d muted", len(ea), len(eb))
	}
}

// A theme's grid notes come through the same rows.
func TestNoteEventsIncludeThemeNotes(t *testing.T) {
	s := NewSynth(DeriveDungeon(1, 2, 0))
	s.PlayTheme(&Theme{Grids: []Grid{{Lead: [Steps]int{1, 0, 3, 0, 5}}}})
	render(s, rows*s.samplesPerRow)
	var rowsWithLead []int
	for _, e := range events(s) {
		if e.Layer == int(trackLead) {
			rowsWithLead = append(rowsWithLead, e.Row)
		}
	}
	if !slices.Equal(rowsWithLead, []int{0, 2, 4}) {
		t.Errorf("lead events on rows %v, want the theme's 0, 2, 4", rowsWithLead)
	}
}

// A mid-pattern mood change moves held notes, and says so.
func TestNoteEventsReportRevoicing(t *testing.T) {
	s := NewSynth(DeriveDungeon(1, 2, 0))
	render(s, 5*s.samplesPerRow+1)
	rust, _ := DeriveMood("rust", 7, 2, 0)
	s.SetParametersNow(rust)
	render(s, nowBlock+1)
	n := 0
	for _, e := range events(s) {
		if e.Kind == NoteRevoice {
			n++
			if e.Pattern != 0 || e.Row > 5 {
				t.Errorf("revoice %+v: want a row of pattern 0 up to the one playing", e)
			}
		}
	}
	if n == 0 {
		t.Error("no revoice events after a SetParametersNow mid-pattern")
	}
}

// A reader that falls more than the ring behind gets the newest events,
// and the count it missed.
func TestNoteEventsOverflow(t *testing.T) {
	p, _ := DeriveMood("forge", 7, 4, 1)
	s := NewSynth(p)
	for s.notes.n.Load() < 3*noteRingSize {
		render(s, s.samplesPerRow)
	}
	n := s.notes.n.Load()
	evs, next, dropped := s.NoteEvents(0, nil)
	if next&cursorCount != n || dropped != n-noteRingSize || len(evs) != noteRingSize {
		t.Fatalf("NoteEvents(0) after %d: %d events, next %d, dropped %d; want %d, %d, %d",
			n, len(evs), next&cursorCount, dropped, noteRingSize, n, n-noteRingSize)
	}
	for i := 1; i < len(evs); i++ {
		if evs[i].Frame < evs[i-1].Frame-int64(s.flamLead()) {
			t.Fatalf("events out of order at %d: %+v after %+v", i, evs[i], evs[i-1])
		}
	}
	// caught up: nothing new, nothing dropped
	if evs, next2, dropped := s.NoteEvents(next, evs[:0]); len(evs) != 0 || next2 != next || dropped != 0 {
		t.Errorf("caught up: %d events, next %d, dropped %d", len(evs), next2, dropped)
	}
}

// A cursor kept across a Stop and Start belongs to the old synth: the new
// one reads from its first event, counting what it no longer holds as
// dropped, whether the old cursor is ahead of the new synth or behind it.
func TestNoteEventsCursorFromAnotherSynth(t *testing.T) {
	p, _ := DeriveMood("forge", 7, 4, 1)
	old := NewSynth(p)
	render(old, 3*old.samplesPerRow)
	_, cursor, _ := old.NoteEvents(0, nil)
	for _, ahead := range []bool{true, false} {
		s := NewSynth(p)
		for want := cursor & cursorCount; ; {
			render(s, s.samplesPerRow)
			if n := s.notes.n.Load(); ahead && n > 0 && n < want || !ahead && n > want+noteRingSize {
				break
			}
		}
		n := s.notes.n.Load()
		evs, _, dropped := s.NoteEvents(cursor, nil)
		if uint64(len(evs))+dropped != n {
			t.Errorf("old cursor %d, new synth at %d (ahead %v): %d events + %d dropped, want all %d",
				cursor&cursorCount, n, ahead, len(evs), dropped, n)
		}
	}
}

// An off 16th armed before a theme cuts the pattern, firing while the
// theme's first row holds back for its flam, belongs to the row that armed
// it, not to the row before the theme's.
func TestNoteEventsRowAcrossAThemeCut(t *testing.T) {
	p, _ := DeriveMood("forge", 7, 4, 1)
	s := NewSynth(p)
	drawn := buildDrums(s.params, 0)
	row, lane := -1, -1
	for r := 2; r < rows && row < 0; r++ {
		for d := range drawn {
			if drawn[d][r].off > 0 {
				row, lane = r, d
				break
			}
		}
	}
	if row < 0 {
		t.Fatal("forge's first pattern has no off 16th to test with")
	}
	for pos, _ := s.Position(); pos.Row != row; pos, _ = s.Position() {
		render(s, 32)
	}
	start := s.rowStart(t, row, 0)
	half := int64(float64(s.samplesPerRow) * (0.5 + s.params.Swing))
	render(s, int(start+half-int64(s.flamLead())/2-s.Frames()))
	s.PlayTheme(&Theme{Grids: []Grid{{Drums: [DrumLanes]string{"g......."}}}})
	render(s, 2*s.flamLead())
	found := false
	for _, e := range events(s) {
		if e.Kind == DrumOff && e.Lane == lane && e.Frame > start {
			found = true
			if e.Row != row || e.Pattern != 0 {
				t.Errorf("off 16th after the cut labelled row %d of pattern %d, want row %d of 0", e.Row, e.Pattern, row)
			}
		}
	}
	if !found {
		t.Error("the armed off 16th never fired")
	}
}

func TestNoteKindNames(t *testing.T) {
	got := fmt.Sprint(NoteOn, NoteRetrigger, NoteRevoice, DrumOn, DrumOff, DrumRatchet, DrumGrace, NoteKind(9))
	if want := "note retrigger revoice on off ratchet grace unknown"; got != want {
		t.Errorf("kinds = %q, want %q", got, want)
	}
}
