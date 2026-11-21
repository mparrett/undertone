package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"testing"

	"github.com/mparrett/undertone/engine"
)

// smfNote is a note read back from a file: on and off in ticks.
type smfNote struct {
	ch, note, vel byte
	on, off       int64
}

type smfFile struct {
	format, division int
	names            []string
	tempo            int // microseconds a quarter
	notes            []smfNote
}

// parseSMF reads the subset of a Standard MIDI File writeSMF writes, and
// fails on anything else: no running status, only note on/off, CC and meta.
func parseSMF(t *testing.T, b []byte) smfFile {
	t.Helper()
	var f smfFile
	if string(b[:4]) != "MThd" || binary.BigEndian.Uint32(b[4:]) != 6 {
		t.Fatalf("no MThd header: % x", b[:8])
	}
	f.format = int(binary.BigEndian.Uint16(b[8:]))
	ntracks := int(binary.BigEndian.Uint16(b[10:]))
	f.division = int(binary.BigEndian.Uint16(b[12:]))
	b = b[14:]
	vlq := func(b []byte, i *int) int64 {
		var v int64
		for {
			c := b[*i]
			*i++
			v = v<<7 | int64(c&0x7f)
			if c&0x80 == 0 {
				return v
			}
		}
	}
	for tr := 0; tr < ntracks; tr++ {
		if string(b[:4]) != "MTrk" {
			t.Fatalf("track %d: no MTrk", tr)
		}
		n := int(binary.BigEndian.Uint32(b[4:]))
		body := b[8 : 8+n]
		b = b[8+n:]
		open := map[[2]byte]int{}
		var at int64
		ended := false
		for i := 0; i < len(body); {
			if ended {
				t.Fatalf("track %d: events after end of track", tr)
			}
			at += vlq(body, &i)
			st := body[i]
			switch {
			case st == 0xff:
				kind := body[i+1]
				i += 2
				l := int(vlq(body, &i))
				data := body[i : i+l]
				i += l
				switch kind {
				case 0x03:
					f.names = append(f.names, string(data))
				case 0x51:
					f.tempo = int(data[0])<<16 | int(data[1])<<8 | int(data[2])
				case 0x2f:
					ended = true
				}
			case st&0xf0 == 0x90 || st&0xf0 == 0x80:
				k := [2]byte{st & 0x0f, body[i+1]}
				if st&0xf0 == 0x90 && body[i+2] > 0 {
					if _, ok := open[k]; ok {
						t.Fatalf("track %d: note %d on ch %d again before its off, at tick %d", tr, k[1], k[0]+1, at)
					}
					open[k] = len(f.notes)
					f.notes = append(f.notes, smfNote{ch: k[0], note: k[1], vel: body[i+2], on: at})
				} else {
					j, ok := open[k]
					if !ok {
						t.Fatalf("track %d: off for note %d on ch %d that is not on, at tick %d", tr, k[1], k[0]+1, at)
					}
					f.notes[j].off = at
					delete(open, k)
				}
				i += 3
			default:
				t.Fatalf("track %d: unexpected status %#x", tr, st)
			}
		}
		if !ended {
			t.Fatalf("track %d: no end of track", tr)
		}
		if len(open) > 0 {
			t.Fatalf("track %d: %d notes never end", tr, len(open))
		}
	}
	if len(b) != 0 {
		t.Fatalf("%d bytes after the last track", len(b))
	}
	return f
}

func renderSMF(t *testing.T, mood string, seconds float64) (smfFile, engine.Params) {
	t.Helper()
	p := deriveParams(mood, 7, 3, 0.5, "")
	msgs, err := renderMIDI(engine.NewSynth(p), p, io.Discard, seconds)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := writeSMF(&buf, p, msgs); err != nil {
		t.Fatal(err)
	}
	return parseSMF(t, buf.Bytes()), p
}

func TestMIDIFileReadsBack(t *testing.T) {
	f, p := renderSMF(t, "forge", 20)
	if f.format != 1 || f.division != ppq {
		t.Fatalf("format %d division %d", f.format, f.division)
	}
	if want := 2 * rowFrames(p) * 1_000_000 / engine.SampleRate; f.tempo < want-1 || f.tempo > want+1 {
		t.Errorf("tempo %d us a quarter, want %d (bpm %.1f)", f.tempo, want, p.BPM)
	}
	if f.names[0] != "undertone" || f.names[len(f.names)-1] != "drums" {
		t.Errorf("track names %v", f.names)
	}
	perCh := map[byte]int{}
	for _, n := range f.notes {
		perCh[n.ch]++
		if n.off <= n.on {
			t.Fatalf("note %+v ends before it starts", n)
		}
		if n.ch == drumChannel && n.note != 36 && n.note != 39 && n.note != 82 {
			t.Errorf("drum note %d is not a lane's GM note", n.note)
		}
		if (n.ch == engine.DrumsLayer || n.ch >= engine.NumLayers) && n.ch != drumChannel {
			t.Errorf("note on channel %d, which no layer uses", n.ch+1)
		}
	}
	// forge has a groove, so drums and the bed both play
	if perCh[drumChannel] == 0 || perCh[0] == 0 || perCh[5] == 0 {
		t.Errorf("notes per channel %v: want drone, pulse and drums", perCh)
	}
	last := int64(0)
	for _, n := range f.notes {
		last = max(last, n.off)
	}
	// 20 s in ticks, and no note left hanging past the render's end
	if end := int64(20 * float64(ppq) * 1e6 / float64(f.tempo)); last > end+1 {
		t.Errorf("last note off at tick %d, past the end at %d", last, end)
	}
}

// Track notes start on rows, and the file's clock is the synth's, so in a
// mood without drums (no flam pulling the first row late) every track note
// lands on an eighth.
func TestMIDITrackNotesLandOnTheGrid(t *testing.T) {
	f, _ := renderSMF(t, "dungeon", 30)
	if len(f.notes) == 0 {
		t.Fatal("no notes")
	}
	for _, n := range f.notes {
		if n.on%(ppq/2) != 0 {
			t.Fatalf("note %+v starts off the eighth-note grid", n)
		}
	}
}

func TestRowFramesMatchesTheSynth(t *testing.T) {
	for _, mood := range []string{"dungeon", "village", "forge"} {
		p := deriveParams(mood, 3, 2, 0.7, "")
		s := engine.NewSynth(p)
		io.CopyN(io.Discard, s, int64(5*rowFrames(p))*4)
		last, ok := s.Position()
		if !ok {
			t.Fatal("no position")
		}
		prev, ok := s.PositionAt(last.Frame - 1)
		if !ok {
			t.Fatal("no previous row")
		}
		if got := last.Frame - prev.Frame; got != int64(rowFrames(p)) {
			t.Errorf("%s: rows are %d frames, rowFrames says %d", mood, got, rowFrames(p))
		}
	}
}

func TestNoteMapperLengths(t *testing.T) {
	p := engine.Params{BPM: 60, LeadGate: 3} // a row is 22050 frames
	m := newNoteMapper(p)
	const row = 22050
	m.add(engine.NoteEvent{Frame: 0, Layer: 3, Note: 60, Gain: 0.5})
	m.add(engine.NoteEvent{Frame: row, Layer: 3, Note: 62, Gain: 0.5}) // cuts the first
	m.add(engine.NoteEvent{Frame: row, Layer: 6, Lane: 0, Gain: 2})    // kick, clipped velocity
	m.add(engine.NoteEvent{Frame: row + 100, Layer: 6, Lane: 0, Gain: 0.1})
	m.flush(10 * row)
	want := []midiMsg{
		{0, [3]byte{0x93, 60, 64}},
		{row, [3]byte{0x83, 60, 0}},
		{row, [3]byte{0x93, 62, 64}},
		{row, [3]byte{0x99, 36, 127}},
		{row + 100, [3]byte{0x89, 36, 0}}, // the next strike cuts it
		{row + 100, [3]byte{0x99, 36, 13}},
		{row + 100 + row/2, [3]byte{0x89, 36, 0}}, // a strike lasts half a row
		{row + 3*row, [3]byte{0x83, 62, 0}},       // the lead's gate: 3 rows
	}
	if len(m.out) != len(want) {
		t.Fatalf("got %d messages %v, want %v", len(m.out), m.out, want)
	}
	for i := range want {
		if m.out[i] != want[i] {
			t.Errorf("message %d: got %v, want %v", i, m.out[i], want[i])
		}
	}
}

func TestVLQ(t *testing.T) {
	for _, c := range []struct {
		v    int64
		want []byte
	}{{0, []byte{0}}, {0x7f, []byte{0x7f}}, {0x80, []byte{0x81, 0}}, {0x3fff, []byte{0xff, 0x7f}}, {0x200000, []byte{0x81, 0x80, 0x80, 0}}} {
		var tb trackBuf
		tb.delta(c.v)
		if !bytes.Equal(tb.b, c.want) {
			t.Errorf("vlq(%#x) = % x, want % x", c.v, tb.b, c.want)
		}
	}
}

// With -wav, WriteWAV reads the whole render at once; the tap has to cut it
// into blocks or the note ring laps. The file is the same as without.
func TestMIDIFileWithWAVMatches(t *testing.T) {
	dir := t.TempDir()
	p := deriveParams("village", 7, 5, 0.6, "")
	if err := writeMIDIFile(dir+"/a.mid", dir+"/a.wav", engine.NewSynth(p), p, 20); err != nil {
		t.Fatal(err)
	}
	if err := writeMIDIFile(dir+"/b.mid", "", engine.NewSynth(p), p, 20); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(dir + "/a.mid")
	b, _ := os.ReadFile(dir + "/b.mid")
	if len(a) == 0 || !bytes.Equal(a, b) {
		t.Errorf("the file rendered with a WAV (%d bytes) differs from the one without (%d)", len(a), len(b))
	}
}

// The engine's notes run -48..180; MIDI's data bytes stop at 127.
func TestMIDINoteFoldsIntoRange(t *testing.T) {
	for _, c := range []struct {
		in   int
		want byte
	}{{60, 60}, {0, 0}, {127, 127}, {-1, 11}, {-48, 0}, {128, 116}, {180, 120}} {
		if got := midiNote(c.in); got != c.want {
			t.Errorf("midiNote(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
