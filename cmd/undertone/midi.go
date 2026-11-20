package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"slices"

	"github.com/mparrett/undertone/engine"
)

// MIDI out: the engine's note events (Synth.NoteEvents) as MIDI messages, for
// a DAW to play through its own instruments. One channel per layer, in
// LayerNames order (drone on 1 ... pulse on 6), and the drums on 10 with a
// General MIDI note per lane.
//
// A note event says when a note starts, not how long it lasts, so the length
// is rebuilt from what the sequencer does: a track's voice holds a note for
// its gate (in rows) and a new note on the same track cuts it, as the
// track's single voice does. A drum strike lasts half a row (a 16th), cut by
// the lane's next strike.

const drumChannel = 9 // channel 10, General MIDI's drums

// gmDrum is the General MIDI percussion note per drum lane (LaneNames): kick
// is Bass Drum 1, clap Hand Clap, shaker Shaker. The lanes are roles, so a
// kit that puts another sound in a lane still lands on its role's note.
var gmDrum = [engine.DrumLanes]byte{36, 39, 82}

// midiMsg is a channel message sounding at an output frame.
type midiMsg struct {
	frame int64
	data  [3]byte
}

// held is a sounding note: the slot holds a track's note or a drum lane's.
type held struct {
	on   bool
	ch   byte
	note byte
	off  int64 // the frame its gate runs out
}

// noteMapper turns note events into note on/off messages, in frame order.
type noteMapper struct {
	gate [engine.NumLayers - 1]int64 // each track's gate, in frames
	hit  int64                       // a drum strike's length, in frames
	held [engine.NumLayers - 1 + engine.DrumLanes]held
	out  []midiMsg
}

// trackGateRows are the gates buildPatterns gives the tracks in generated
// music, by layer; the lead's is Params.LeadGate. The engine does not report
// them, so they are copied from buildPatterns and nothing checks the copy: a
// gate changed there needs changing here.
var trackGateRows = [engine.NumLayers - 1]int{16, 16, 6, 0, 10, 1}

func newNoteMapper(p engine.Params) *noteMapper {
	spr := int64(rowFrames(p))
	m := &noteMapper{hit: spr / 2}
	for i, g := range trackGateRows {
		if i == 3 {
			g = max(1, p.LeadGate)
		}
		m.gate[i] = int64(g) * spr
	}
	return m
}

// rowFrames is a row's length in frames, as the synth counts it (two rows a
// beat, truncated to whole frames). Timing the file by it rather than by
// the bpm keeps every row on its tick over minutes.
func rowFrames(p engine.Params) int {
	bpm := min(max(p.BPM, 20), 400)
	return int(engine.SampleRate * 60 / (bpm * 2))
}

// velocity maps an event's gain to a MIDI velocity. Gains above 1 (drums
// with a hot DrumGain) clip at 127; a heard note never goes below 1, which
// would be a note-off.
func velocity(gain float64) byte {
	return byte(max(1, min(127, math.Round(gain*127))))
}

// release ends the slot's note at frame.
func (m *noteMapper) release(slot int, frame int64) {
	h := &m.held[slot]
	if h.on {
		m.out = append(m.out, midiMsg{frame, [3]byte{0x80 | h.ch, h.note, 0}})
		h.on = false
	}
}

// advance ends every note whose gate runs out by frame, earliest first.
func (m *noteMapper) advance(frame int64) {
	for {
		slot := -1
		for i, h := range m.held {
			if h.on && h.off <= frame && (slot < 0 || h.off < m.held[slot].off) {
				slot = i
			}
		}
		if slot < 0 {
			return
		}
		m.release(slot, m.held[slot].off)
	}
}

// midiNote folds a voice's note (minNote..maxNote, wider than MIDI's) into
// 0..127 by octaves, keeping its pitch class. Unfolded, a note past 127 or
// under 0 would set a data byte's top bit, which a receiver reads as a status
// byte, garbling the rest of the stream.
func midiNote(n int) byte {
	for n < 0 {
		n += 12
	}
	for n > 127 {
		n -= 12
	}
	return byte(n)
}

// add plays e, ending the note its slot held first.
func (m *noteMapper) add(e engine.NoteEvent) {
	m.advance(e.Frame)
	var slot int
	var h held
	if e.Layer == engine.NumLayers-1 {
		slot = len(m.gate) + e.Lane
		h = held{ch: drumChannel, note: gmDrum[e.Lane], off: e.Frame + m.hit}
	} else {
		slot = e.Layer
		h = held{ch: byte(e.Layer), note: midiNote(e.Note), off: e.Frame + m.gate[e.Layer]}
	}
	m.release(slot, e.Frame)
	h.on = true
	m.held[slot] = h
	m.out = append(m.out, midiMsg{e.Frame, [3]byte{0x90 | h.ch, h.note, velocity(e.Gain)}})
}

// flush ends every note still sounding at frame.
func (m *noteMapper) flush(frame int64) {
	m.advance(frame)
	for i := range m.held {
		m.release(i, frame)
	}
}

// noteTap reads a synth and maps the note events behind each read. The ring
// the events wait in holds 256, so a read is cut to a block: one read of a
// whole render (WriteWAV makes one) would lap it.
type noteTap struct {
	s      *engine.Synth
	m      *noteMapper
	cursor uint64
	evs    []engine.NoteEvent
	err    error
}

const tapBlock = 1024 * 4 // bytes: 1024 frames, 23 ms

func (t *noteTap) Read(p []byte) (int, error) {
	if len(p) > tapBlock {
		p = p[:tapBlock]
	}
	n, err := t.s.Read(p)
	t.drain()
	return n, err
}

func (t *noteTap) drain() {
	evs, next, dropped := t.s.NoteEvents(t.cursor, t.evs[:0])
	t.cursor, t.evs = next, evs
	if dropped > 0 && t.err == nil {
		t.err = fmt.Errorf("midi: %d note events were overwritten before they were read", dropped)
	}
	// a drum lane's countdown logs its strike after the row's notes in the
	// same block, so a block's events are not always in frame order
	slices.SortStableFunc(evs, func(a, b engine.NoteEvent) int { return int(a.Frame - b.Frame) })
	for _, e := range evs {
		t.m.add(e)
	}
	t.m.advance(t.s.Frames())
}

// renderMIDI renders seconds of s to w (io.Discard for MIDI alone), and
// returns the notes it played. s must be fresh: the file starts at frame 0.
func renderMIDI(s *engine.Synth, p engine.Params, w io.Writer, seconds float64) ([]midiMsg, error) {
	t := &noteTap{s: s, m: newNoteMapper(p)}
	if _, err := io.CopyN(w, t, int64(seconds*engine.SampleRate)*4); err != nil {
		return nil, err
	}
	t.m.flush(s.Frames())
	return t.m.out, t.err
}

// ppq is the file's ticks per quarter note; a row (an eighth) is ppq/2.
const ppq = 480

// writeSMF writes msgs as a format-1 Standard MIDI File: a tempo track, then
// one track per channel that plays, named for its layer. The tempo is the
// synth's row length rather than the rounded bpm, so a tick count from a
// frame lands on the grid and the file lines up with a WAV of the same
// render.
func writeSMF(w io.Writer, p engine.Params, msgs []midiMsg) error {
	msgs = slices.Clone(msgs)
	slices.SortStableFunc(msgs, func(a, b midiMsg) int { return int(a.frame - b.frame) })
	spr := int64(rowFrames(p))
	tick := func(frame int64) int64 { return (frame*ppq/2 + spr/2) / spr }
	usPerQuarter := (2*spr*1_000_000 + engine.SampleRate/2) / engine.SampleRate

	var tracks [][]byte
	var tempo trackBuf
	tempo.meta(0, 0x03, []byte("undertone"))
	tempo.meta(0, 0x51, []byte{byte(usPerQuarter >> 16), byte(usPerQuarter >> 8), byte(usPerQuarter)})
	tempo.meta(0, 0x58, []byte{4, 2, 24, 8}) // 4/4
	tempo.end(0)
	tracks = append(tracks, tempo.b)

	names := engine.LayerNames()
	for ch := byte(0); ch < 16; ch++ {
		var t trackBuf
		last := int64(0)
		for _, m := range msgs {
			if m.data[0]&0x0f != ch {
				continue
			}
			if t.b == nil {
				name := names[len(names)-1]
				if ch != drumChannel {
					name = names[ch]
				}
				t.meta(0, 0x03, []byte(name))
			}
			at := tick(m.frame)
			t.event(at-last, m.data[:])
			last = at
		}
		if t.b != nil {
			t.end(0)
			tracks = append(tracks, t.b)
		}
	}

	bw := bufio.NewWriter(w)
	// bufio keeps its first error, which Flush returns
	bw.WriteString("MThd")
	binary.Write(bw, binary.BigEndian, uint32(6))
	binary.Write(bw, binary.BigEndian, [3]uint16{1, uint16(len(tracks)), ppq})
	for _, t := range tracks {
		bw.WriteString("MTrk")
		binary.Write(bw, binary.BigEndian, uint32(len(t)))
		bw.Write(t)
	}
	return bw.Flush()
}

// trackBuf builds one MTrk chunk's body.
type trackBuf struct{ b []byte }

func (t *trackBuf) delta(d int64) {
	// a variable-length quantity: 7 bits a byte, high bit set on all but
	// the last
	var tmp [10]byte
	i := len(tmp) - 1
	tmp[i] = byte(d & 0x7f)
	for d >>= 7; d > 0; d >>= 7 {
		i--
		tmp[i] = byte(d&0x7f) | 0x80
	}
	t.b = append(t.b, tmp[i:]...)
}

func (t *trackBuf) event(d int64, data []byte) {
	t.delta(d)
	t.b = append(t.b, data...)
}

func (t *trackBuf) meta(d int64, kind byte, data []byte) {
	t.delta(d)
	t.b = append(t.b, 0xff, kind)
	t.delta(int64(len(data)))
	t.b = append(t.b, data...)
}

func (t *trackBuf) end(d int64) { t.meta(d, 0x2f, nil) }

// writeMIDIFile renders seconds of s to a MIDI file at path, and to a WAV
// at wavPath too unless it is empty.
func writeMIDIFile(path, wavPath string, s *engine.Synth, p engine.Params, seconds float64) error {
	var msgs []midiMsg
	var err error
	if wavPath != "" {
		t := &noteTap{s: s, m: newNoteMapper(p)}
		if err = engine.WriteWAV(wavPath, t, seconds); err == nil {
			t.m.flush(s.Frames())
			msgs, err = t.m.out, t.err
		}
	} else {
		msgs, err = renderMIDI(s, p, io.Discard, seconds)
	}
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return errors.Join(writeSMF(f, p, msgs), f.Close())
}
