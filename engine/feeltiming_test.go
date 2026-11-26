package engine

import (
	"math"
	"testing"
)

// lateOf is how many frames after its row each lead and bass note of evs
// started, by layer.
func lateOf(evs []NoteEvent, spr int) map[int][]int64 {
	out := map[int][]int64{}
	for _, e := range evs {
		if e.Layer == 3 || e.Layer == 7 {
			out[e.Layer] = append(out[e.Layer], e.Frame-int64((e.Pattern*rows+e.Row)*spr))
		}
	}
	return out
}

// With Feel, an authored note starts a little after its row, never before
// it, and not every note by the same amount. Without, each starts on its
// row's first frame.
func TestFeelStartsNotesLate(t *testing.T) {
	p := DeriveOverworld(3, 2, 0)
	spr := NewSynth(p).samplesPerRow
	for _, feel := range []float64{0, 0.5, 1} {
		_, evs := renderFeel(p, feel, feelGrid())
		// the most a note waits: the base, the wander's limit, the offbeat's lean and the bass's
		limit := int64(feel * (lateBaseMS + lateWanderMS + lateOffbeatMS + lateBassMS) * SampleRate / 1000)
		for layer, late := range lateOf(evs, spr) {
			lo, hi := late[0], late[0]
			for _, d := range late {
				lo, hi = min(lo, d), max(hi, d)
			}
			switch {
			case feel == 0 && (lo != 0 || hi != 0):
				t.Errorf("feel 0, layer %d: notes start %d to %d frames after their rows, want 0", layer, lo, hi)
			case feel > 0 && (lo < 0 || hi > limit || lo == hi):
				t.Errorf("feel %v, layer %d: notes start %d to %d frames after their rows, want a spread within 0..%d", feel, layer, lo, hi, limit)
			}
		}
	}
}

// The lateness has the shape it is described as: within its limits, the
// offbeats later than the beats, the bass behind the lead, and a wander that
// carries from one row to the next instead of jumping about.
func TestLateMSShape(t *testing.T) {
	const n = 4000
	var beat, off, bass, lead float64
	var prev, sumXY, sumX, sumXX float64
	for at := range n {
		l, b := lateMS(11, laneLead, at), lateMS(11, laneBass, at)
		for _, ms := range []float64{l, b - lateBassMS} {
			base := ms
			if at%2 == 1 {
				base -= lateOffbeatMS
			}
			if base < lateBaseMS-lateWanderMS-1e-9 || base > lateBaseMS+lateWanderMS+1e-9 {
				t.Fatalf("row %d: %v ms is outside the wander's limits", at, ms)
			}
		}
		if at%2 == 1 {
			off += l
		} else {
			beat += l
		}
		lead, bass = lead+l, bass+b
		// the lead's wander alone, for its correlation from row to row
		w := l - lateBaseMS
		if at%2 == 1 {
			w -= lateOffbeatMS
		}
		if at > 0 {
			sumXY += w * prev
		}
		sumX, sumXX, prev = sumX+w, sumXX+w*w, w
	}
	if d := (off - beat) / (n / 2); math.Abs(d-lateOffbeatMS) > 0.5 {
		t.Errorf("offbeats are %.2f ms later than beats on average, want about %v", d, lateOffbeatMS)
	}
	if d := (bass - lead) / n; math.Abs(d-lateBassMS) > 0.5 {
		t.Errorf("the bass is %.2f ms behind the lead on average, want about %v", d, lateBassMS)
	}
	mean := sumX / n
	if math.Abs(mean) > 0.5 {
		t.Errorf("the wander's mean is %.2f ms, want about 0", mean)
	}
	if corr := (sumXY/n - mean*mean) / (sumXX/n - mean*mean); corr < 0.6 || corr > 0.95 {
		t.Errorf("the wander's correlation from row to row is %.2f, want about %v", corr, lateCarry)
	}
	if a, b := lateMS(11, laneLead, 40), lateMS(11, laneLead, 40); a != b {
		t.Errorf("the same place drew %v then %v", a, b)
	}
}

// A row shorter than any tempo allows still holds a note's lateness to a
// third of it, so the note has started before its lane steps again.
func TestLateIsHeldToAThirdOfTheRow(t *testing.T) {
	p := DeriveOverworld(3, 2, 0)
	p.Feel = 1
	s := NewSynth(p)
	s.samplesPerRow = 600
	held := 0
	for row := range rows {
		s.feel(laneBass, 0, row)
		if d := s.lateBy[laneBass][row]; d > 200 {
			t.Errorf("row %d: %d frames late in a row of 600", row, d)
		} else if d == 200 {
			held++
		}
	}
	if held == 0 {
		t.Error("no row's lateness reached the limit, so the limit went untested")
	}
}

// At the slowest and fastest tempos Params allows, every lateness fits its
// row with room to spare, and nothing is left waiting a row later.
func TestLateFitsTheRow(t *testing.T) {
	for _, bpm := range []float64{60, 400} {
		p := DeriveOverworld(3, 2, 0)
		p.BPM, p.Feel = bpm, 1
		s := NewSynth(p)
		s.SetGrid(feelGrid())
		buf := make([]byte, patternBytes(p))
		s.Read(buf)
		for lane := range s.lateBy {
			for row, d := range s.lateBy[lane] {
				if d < 0 || d > s.samplesPerRow/3 {
					t.Errorf("%v bpm, lane %d row %d: %d frames late in a row of %d", bpm, lane, row, d, s.samplesPerRow)
				}
			}
		}
		if n := s.late[laneLead].left + s.late[laneBass].left; n >= s.samplesPerRow {
			t.Errorf("%v bpm: a note waits %d frames", bpm, n)
		}
	}
}

// A SetParametersNow while a note waits leaves it waiting: it starts once,
// when it was going to, and is not struck early by the redraw.
func TestRedrawLeavesAWaitingNoteWaiting(t *testing.T) {
	p := DeriveOverworld(3, 2, 0)
	p.Feel = 1
	g := &Grid{}
	g.Lead[2], g.Lead[4] = 3, 5
	s := NewSynth(p)
	s.SetGrid(g)
	buf := make([]byte, 4)
	for s.stepRow != 4 {
		s.Read(buf)
	}
	n := s.late[laneLead]
	if n.left == 0 || n.row != 4 {
		t.Fatalf("no note waiting on row 4: %+v", n)
	}
	due := s.nframe + int64(n.left) - 1 // it starts on the frame that counts the last one off
	s.applyNow(&paramCmd{p: s.params})
	if got := s.late[laneLead]; got.left != n.left || got.tr.note != n.tr.note {
		t.Fatalf("the waiting note changed from %+v to %+v", n, got)
	}
	for range s.samplesPerRow / 2 {
		s.Read(buf)
	}
	evs, _, _ := s.NoteEvents(0, nil)
	var row4 []NoteEvent
	for _, e := range evs {
		if e.Layer == 3 && e.Row == 4 {
			row4 = append(row4, e)
		}
	}
	if len(row4) != 1 || row4[0].Kind == NoteRevoice {
		t.Fatalf("row 4's note started %d times: %+v", len(row4), row4)
	}
	if row4[0].Frame != due {
		t.Errorf("row 4's note started at frame %d, was due at %d", row4[0].Frame, due)
	}
}

// A note of a pattern cut by a restart, still waiting, never starts.
func TestRestartDropsAWaitingNote(t *testing.T) {
	p := DeriveOverworld(3, 2, 0)
	p.Feel = 1
	g := &Grid{}
	g.Lead[4] = 3
	s := NewSynth(p)
	s.SetGrid(g)
	buf := make([]byte, 4)
	for s.stepRow != 4 {
		s.Read(buf)
	}
	if s.late[laneLead].left == 0 {
		t.Fatal("no note waiting")
	}
	s.startOver()
	if s.late[laneLead].left != 0 || s.late[laneBass].left != 0 {
		t.Errorf("a note of the cut pattern is still waiting: %+v", s.late)
	}
}

// A mood change while a note waits does not strike the note before it
// afresh in the new voice: the waiting note brings the new voice in, once.
func TestMoodChangeWhileWaitingStrikesOnce(t *testing.T) {
	p := DeriveOverworld(3, 2, 0)
	p.Feel = 1
	g := &Grid{}
	g.Bass[3], g.Bass[4] = 1, 5 // the bass's gate is two rows: row 3's is still open at row 4
	s := NewSynth(p)
	s.SetGrid(g)
	buf := make([]byte, 4)
	for s.stepRow != 4 {
		s.Read(buf)
	}
	if s.late[laneBass].left == 0 {
		t.Fatal("no bass note waiting on row 4")
	}
	q := s.params
	ApplyBass(&q, "bell")
	s.applyNow(&paramCmd{p: q.owned()})
	if v := &s.voices[trackBass]; v.pend.note != 0 || !samePatch(v.patch, p.Palette.bass()) {
		t.Errorf("row 3's note was struck again in the new voice")
	}
	for range s.samplesPerRow / 2 {
		s.Read(buf)
	}
	evs, _, _ := s.NoteEvents(0, nil)
	var got []NoteEvent
	for _, e := range evs {
		if e.Layer == 7 && e.Row >= 3 {
			got = append(got, e)
		}
	}
	if len(got) != 2 || got[0].Row != 3 || got[1].Row != 4 || got[1].Kind == NoteRevoice {
		t.Fatalf("bass events from row 3 on: %+v, want row 3's note and row 4's, once each", got)
	}
	if v := &s.voices[trackBass]; !samePatch(v.patch, q.Palette.bass()) && !samePatch(v.pend.patch, q.Palette.bass()) {
		t.Error("row 4's note did not bring the new voice in")
	}
}

// Notes on consecutive rows join with Feel as they do without: the one
// sounding keeps its gate until the late one starts, with no release between.
func TestConsecutiveNotesJoinWithFeel(t *testing.T) {
	p := DeriveOverworld(3, 2, 0)
	ApplyLead(&p, "reed")
	p.LeadGate, p.Feel = 1, 1
	g := &Grid{}
	g.Lead[2], g.Lead[3] = 3, 4
	s := NewSynth(p)
	s.SetGrid(g)
	buf := make([]byte, 4)
	for s.stepRow != 3 {
		s.Read(buf)
	}
	v := &s.voices[trackLead]
	for s.late[laneLead].left > 1 {
		if !v.gate {
			t.Fatalf("row 2's note was let go %d frames before row 3's starts", s.late[laneLead].left)
		}
		s.Read(buf)
	}
	// and a note with nothing after it still ends when its gate runs out
	for s.stepRow != 5 {
		s.Read(buf)
	}
	if v.gate {
		t.Error("row 3's note, one row long, is still held at row 5")
	}
}
