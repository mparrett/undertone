package engine

import (
	"math"
	"sync/atomic"
)

// NoteEvent is a note the music started: a track's note on a row, or a drum
// strike. One-shots are not music and never appear.
type NoteEvent struct {
	Frame int64 // the frame it starts on
	Layer int   // LayerNames index
	Kind  NoteKind
	Lane  int // the drum lane, a LaneNames index, for drums; else 0
	Note  int // MIDI
	Gain  float64
	// the row and pattern it belongs to; a flam's grace belongs to the row
	// it leads into
	Row, Pattern int
}

// NoteKind is what started a note.
type NoteKind uint8

const (
	NoteOn        NoteKind = iota // a track's note on its row
	NoteRetrigger                 // the same, fading the voice's last note out first: heard 5 ms late
	NoteRevoice                   // a SetParametersNow moving a held note to the redrawn pattern
	DrumOn                        // a drum lane's on 16th
	DrumOff                       // its off 16th
	DrumRatchet                   // a ratchet's repeat
	DrumGrace                     // a flam's grace note
)

var noteKindNames = [...]string{"note", "retrigger", "revoice", "on", "off", "ratchet", "grace"}

// String is the kind's name: note, retrigger, revoice, on, off, ratchet,
// grace.
func (k NoteKind) String() string {
	if int(k) < len(noteKindNames) {
		return noteKindNames[k]
	}
	return "unknown"
}

// noteRingSize holds four or more patterns of a typical mood (20 to 60
// events each) and about eight rows of the densest (every drum lane
// ratcheting at 400 bpm).
const noteRingSize = 256

// noteRing is a single-writer ring of events. The audio thread writes and
// never waits; a reader keeps its own cursor, so the synth holds no reader
// state and any number can read. Overflow overwrites the oldest, and a
// reader the writer laps is told how many it missed. It has a slot more
// than the events it promises: a reader gives up the oldest slot as
// possibly mid-overwrite.
type noteRing struct {
	frame, pat, packed [noteSlots]atomic.Uint64
	n                  atomic.Uint64 // events written
	epoch              uint64        // this synth's mark on its cursors; set before the synth is shared
}

const noteSlots = noteRingSize + 1

// A cursor carries the synth it came from above bit 40 (12 bits), so one
// kept across a Stop and Start is known for another synth's whatever its
// count; the count of events read sits below. Both fit 52 bits, so a cursor
// survives lg numbers that pass through a float64.
const (
	cursorShift = 40
	cursorCount = 1<<cursorShift - 1
	epochMask   = 1<<12 - 1
)

var noteEpochs atomic.Uint64

// newEpoch marks a new synth's cursors: never 0, so a cursor of 0 starts
// any synth from its oldest event.
func newEpoch() uint64 {
	for {
		if e := noteEpochs.Add(1) & epochMask; e != 0 {
			return e
		}
	}
}

// packed: gain as float32 bits (32), note (7), row (4), lane (2), kind (3),
// layer (3).
func (r *noteRing) add(e NoteEvent) {
	n := r.n.Load()
	i := n % noteSlots
	r.frame[i].Store(uint64(e.Frame))
	r.pat[i].Store(uint64(e.Pattern))
	r.packed[i].Store(uint64(math.Float32bits(float32(e.Gain))) |
		uint64(e.Note&0x7f)<<32 | uint64(e.Row&0xf)<<39 | uint64(e.Lane&3)<<43 |
		uint64(e.Kind&7)<<45 | uint64(e.Layer&7)<<48)
	r.n.Store(n + 1)
}

// NoteEvents appends the events after cursor since to dst and returns them,
// with the cursor to pass next time and how many events since that cursor
// were overwritten before they could be read. Start from 0. A cursor from
// another synth (one replaced by a Stop and Start) reads this one from its
// first event. Safe to call from any goroutine; it allocates only if dst is
// too small.
func (s *Synth) NoteEvents(cursor uint64, dst []NoteEvent) (evs []NoteEvent, next, dropped uint64) {
	r := &s.notes
	n := r.n.Load()
	since := cursor & cursorCount
	if cursor>>cursorShift != r.epoch || since > n {
		since = 0
	}
	if oldest := n - min(n, noteRingSize); since < oldest {
		dropped, since = oldest-since, oldest
	}
	for k := since; k < n; k++ {
		i := k % noteSlots
		f, pt, pk := r.frame[i].Load(), r.pat[i].Load(), r.packed[i].Load()
		// the writer may have begun overwriting slot k (a ring behind the
		// event it is writing): the three words may not belong together
		if k+noteSlots <= r.n.Load() {
			dropped++
			continue
		}
		dst = append(dst, NoteEvent{
			Frame:   int64(f),
			Pattern: int(pt),
			Gain:    float64(math.Float32frombits(uint32(pk))),
			Note:    int(pk >> 32 & 0x7f),
			Row:     int(pk >> 39 & 0xf),
			Lane:    int(pk >> 43 & 3),
			Kind:    NoteKind(pk >> 45 & 7),
			Layer:   int(pk >> 48 & 7),
		})
	}
	return dst, r.epoch<<cursorShift | n, dropped
}

// noteAt logs a track's note started at the frame about to render, on row
// of pat.
func (s *Synth) noteAt(t int, kind NoteKind, note int, gain float64, row, pat int) {
	s.notes.add(NoteEvent{Frame: s.nframe, Layer: t, Kind: kind, Note: clampNote(note), Gain: gain, Row: row, Pattern: pat})
}

// hit strikes drum lane d and logs it. Strikes come from the row step and
// the flam lead-in, before the frame counter moves on, and from the drum
// lanes' countdowns, after it has: frame is the frame the strike sounds on,
// and row and pat the row it belongs to.
func (s *Synth) hit(d int, kind NoteKind, k drumSound, gain float64, seed uint64, frame int64, row, pat int) {
	s.dvoices[d].strike(k, gain, seed)
	s.notes.add(NoteEvent{Frame: frame, Layer: layerDrums, Kind: kind, Lane: d, Note: clampNote(k.note), Gain: gain, Row: row, Pattern: pat})
}

// LaneNames names the drum lanes, the index NoteEvent.Lane uses. They are
// roles more than sounds: a palette's kit may put another sound in a lane.
func LaneNames() [DrumLanes]string { return laneNames }
