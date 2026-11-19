package engine

import (
	"bytes"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// kinds counts the note events of each kind logged after cursor.
func kinds(s *Synth, cursor uint64) map[NoteKind]int {
	evs, _, _ := s.NoteEvents(cursor, nil)
	n := map[NoteKind]int{}
	for _, e := range evs {
		n[e.Kind]++
	}
	return n
}

// An off 16th, or its flam grace, armed before an immediate tempo change
// still plays in its row, whether the row shrinks or stretches.
func TestTempoChangeKeepsArmedDrumHits(t *testing.T) {
	for _, c := range []struct {
		name     string
		lane     string
		kind     NoteKind
		from, to float64
	}{
		{"off faster", ".x", DrumOff, 60, 240},
		{"off slower", ".x", DrumOff, 240, 60},
		{"grace faster", ".g", DrumGrace, 60, 240},
		{"grace slower", ".g", DrumGrace, 240, 60},
	} {
		p := DeriveDungeon(7, 2, 0)
		p.BPM = c.from
		s := NewSynth(p)
		s.SetGrid(&Grid{Drums: [3]string{c.lane}})
		render(s, 1)
		for s.offLeft[0] == 0 { // until the grid's first row has armed it
			render(s, 1)
		}
		_, cursor, _ := s.NoteEvents(0, nil)
		q := p
		q.BPM = c.to
		s.SetParametersNow(q)
		render(s, nowBlock)
		render(s, s.rowLeft)
		if got := kinds(s, cursor)[c.kind]; got != 1 {
			t.Errorf("%s: %d %v hits in the changed row, want 1", c.name, got, c.kind)
		}
		if c.kind == DrumGrace {
			at := map[NoteKind]int64{}
			evs, _, _ := s.NoteEvents(cursor, nil)
			for _, e := range evs {
				at[e.Kind] = e.Frame
			}
			if gap := at[DrumOff] - at[DrumGrace]; gap != int64(s.flamLead()) {
				t.Errorf("%s: grace %d frames ahead of its off 16th, want flamLead %d", c.name, gap, s.flamLead())
			}
		}
	}
}

// A ratchet part-way through its repeats when the tempo changes plays the
// rest of them before the row steps.
func TestTempoChangeKeepsRatchetRepeats(t *testing.T) {
	p := DeriveDungeon(7, 2, 0)
	p.BPM = 60
	s := NewSynth(p)
	s.SetGrid(&Grid{Drums: [3]string{"x"}})
	render(s, 1)
	for s.row != 1 { // just past the grid's first row's step
		render(s, 1)
	}
	s.rat[0].arm(4, s.rowLeft, 1, 1)
	_, cursor, _ := s.NoteEvents(0, nil)
	q := p
	q.BPM = 240
	s.SetParametersNow(q)
	render(s, nowBlock)
	render(s, s.rowLeft)
	if got := kinds(s, cursor)[DrumRatchet]; got != 3 {
		t.Errorf("%d ratchet repeats in the changed row, want 3", got)
	}
}

// Turning the bed off mid-pattern closes its held gates at the next row,
// not at the pattern's end.
func TestBedOffMidPatternReleasesAtNextRow(t *testing.T) {
	s := NewSynth(DeriveDungeon(7, 2, 0))
	render(s, 1)
	if !s.voices[0].gate {
		t.Fatal("the drone is not held at the first row")
	}
	s.SetGrid(&Grid{Bed: false})
	render(s, s.rowLeft+1)
	for _, tr := range []int{0, 2, 4} {
		if s.voices[tr].gate {
			t.Errorf("track %d still gated at row %d", tr, s.row)
		}
	}
}

// The event ring carries the voice's whole note range.
func TestNoteEventsCarryFullNoteRange(t *testing.T) {
	for _, note := range []int{minNote, -1, 0, 127, 128, 140, maxNote} {
		s := NewSynth(DeriveDungeon(7, 2, 0))
		_, cursor, _ := s.NoteEvents(0, nil)
		s.noteAt(trackLead, NoteOn, note, 0.5, 3, 2)
		evs, _, _ := s.NoteEvents(cursor, nil)
		e := evs[len(evs)-1]
		if e.Note != note || e.Row != 3 || e.Layer != trackLead || e.Kind != NoteOn || e.Lane != 0 {
			t.Errorf("logged note %d, read %+v", note, e)
		}
	}
}

// Reads of any length make progress and concatenate to the aligned stream.
func TestReadAnyLength(t *testing.T) {
	const total = 4 * 1000
	want := render(NewSynth(DeriveDungeon(7, 2, 0)), total/4)
	for _, size := range []int{1, 2, 3, 5, 7} {
		s := NewSynth(DeriveDungeon(7, 2, 0))
		var got []byte
		buf := make([]byte, size)
		for len(got) < total {
			n, err := s.Read(buf[:min(size, total-len(got))])
			if n == 0 || err != nil {
				t.Fatalf("size %d: Read = %d, %v after %d bytes", size, n, err, len(got))
			}
			got = append(got, buf[:n]...)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("size %d: stream differs from aligned reads", size)
		}
		if s.Frames() != total/4 {
			t.Errorf("size %d: Frames = %d, want %d", size, s.Frames(), total/4)
		}
	}
	if _, err := io.ReadFull(NewSynth(DeriveDungeon(7, 2, 0)), make([]byte, 7)); err != nil {
		t.Errorf("io.ReadFull of 7 bytes: %v", err)
	}
	// Frames counts a split frame once its last byte is out
	s := NewSynth(DeriveDungeon(7, 2, 0))
	for _, c := range []struct{ read, frames int }{{6, 1}, {1, 1}, {1, 2}, {4, 3}} {
		s.Read(make([]byte, c.read))
		if s.Frames() != int64(c.frames) {
			t.Errorf("after reading %d more bytes: Frames = %d, want %d", c.read, s.Frames(), c.frames)
		}
	}
}

// A bad duration is an error before anything is read or written; a good one
// writes the stream after a 44-byte header; a source that ends early leaves
// no file.
func TestWriteWAVDurations(t *testing.T) {
	dir := t.TempDir()
	for _, sec := range []float64{-1, math.NaN(), math.Inf(1), 1e9} {
		path := filepath.Join(dir, "bad.wav")
		if err := WriteWAV(path, NewSynth(DeriveDungeon(7, 2, 0)), sec); err == nil {
			t.Errorf("WriteWAV(%v s) = nil, want an error", sec)
		}
		if _, err := os.Stat(path); err == nil {
			t.Errorf("WriteWAV(%v s) left a file", sec)
		}
	}

	path := filepath.Join(dir, "ok.wav")
	if err := WriteWAV(path, NewSynth(DeriveDungeon(7, 2, 0)), 0.5); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := render(NewSynth(DeriveDungeon(7, 2, 0)), SampleRate/2)
	if len(b) != 44+len(want) || !bytes.Equal(b[44:], want) {
		t.Errorf("wrote %d bytes, want a 44-byte header and the %d-byte stream", len(b), len(want))
	}

	short := filepath.Join(dir, "short.wav")
	if err := WriteWAV(short, bytes.NewReader(make([]byte, 100)), 1); err == nil {
		t.Error("a source ending early: nil error")
	}
	if _, err := os.Stat(short); err == nil {
		t.Error("a source ending early left a file")
	}
}
