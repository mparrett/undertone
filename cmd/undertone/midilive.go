//go:build !js

package main

import (
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mparrett/undertone/engine"
)

// midiSink sends channel messages now. The live loop times them itself: a
// receiver may ignore CoreMIDI timestamps and play a message when it
// arrives, so a message stamped for later would play early.
type midiSink interface {
	send(data [3]byte) error
	close()
}

// liveAhead is how far the synth renders ahead of the clock, so a note's
// length is known (the next note on its track cuts it) before its off is
// due.
const liveAhead = 60 * time.Millisecond

// liveLayers are the layers -midi-live sends, all of them unless -layer names
// some: a receiver that plays every channel on one instrument, as Slab does,
// gets one clean part rather than the whole mix piled on it.
var liveLayers = func() (l [engine.NumLayers]bool) {
	for i := range l {
		l[i] = true
	}
	return l
}()

// setLayers limits liveLayers to the named layers, comma-separated.
func setLayers(list string) error {
	if list == "" {
		return nil
	}
	var l [engine.NumLayers]bool
	names := engine.LayerNames()
	for _, n := range strings.Split(list, ",") {
		n = strings.TrimSpace(n)
		i := slices.Index(names[:], n)
		if i < 0 {
			return fmt.Errorf("-layer %q: want %s", n, strings.Join(names[:], ", "))
		}
		l[i] = true
	}
	liveLayers = l
	return nil
}

// layerChannel is the channel a layer's notes are on, as noteMapper sends
// them.
func layerChannel(layer int) byte {
	if layer == engine.DrumsLayer {
		return drumChannel
	}
	return byte(layer)
}

// liveSource is one virtual source and the channels it gets all-notes-off on
// at the end.
type liveSource struct {
	name  string
	sink  midiSink
	chans []byte
}

// liveRoute says which source each channel's messages go to: nil drops them.
type liveRoute struct {
	sources []liveSource
	byChan  [16]midiSink
}

// sourceName is a layer's source under -midi-split.
func sourceName(layer int) string { return "undertone " + engine.LayerNames()[layer] }

// openRoute opens the sources -midi-live sends to. Without split it is the
// one "undertone" source, a channel per layer. With split it is a source per
// layer in layers, named for it, each layer on the channel it has without
// split: a receiver that picks a source gets the layer alone, and one that
// reads only channels hears what it did before. A layer -layer leaves out
// gets no source, so a receiver lists only the sources that play.
func openRoute(split bool, layers [engine.NumLayers]bool, open func(string) (midiSink, error)) (*liveRoute, error) {
	r := &liveRoute{}
	if !split {
		sink, err := open("undertone")
		if err != nil {
			return nil, err
		}
		// all-notes-off goes to every channel this tool uses, filtered by
		// -layer or not, as it did before sources could split.
		var chans []byte
		for ch := byte(0); ch < engine.NumLayers; ch++ {
			chans = append(chans, ch)
		}
		r.sources = []liveSource{{"undertone", sink, append(chans, drumChannel)}}
		for i, on := range layers {
			if on {
				r.byChan[layerChannel(i)] = sink
			}
		}
		return r, nil
	}
	for i, on := range layers {
		if !on {
			continue
		}
		name := sourceName(i)
		sink, err := open(name)
		if err != nil {
			r.close()
			return nil, err
		}
		ch := layerChannel(i)
		r.sources = append(r.sources, liveSource{name, sink, []byte{ch}})
		r.byChan[ch] = sink
	}
	return r, nil
}

// send sends data to its channel's source, or drops it.
func (r *liveRoute) send(data [3]byte) error {
	if sink := r.byChan[data[0]&0x0f]; sink != nil {
		return sink.send(data)
	}
	return nil
}

func (r *liveRoute) close() {
	for _, s := range r.sources {
		s.sink.close()
	}
}

// names quotes the sources' names for a message.
func (r *liveRoute) names() string {
	q := make([]string, len(r.sources))
	for i, s := range r.sources {
		q[i] = strconv.Quote(s.name)
	}
	return strings.Join(q, ", ")
}

// playMIDILive renders s in real time, without audio, and sends its notes
// to virtual MIDI sources (see openRoute) until interrupted.
func playMIDILive(s *engine.Synth, p engine.Params, desc string, split bool) error {
	out, err := openRoute(split, liveLayers, openVirtualSource)
	if err != nil {
		return err
	}
	defer out.close()
	noun := "source"
	if len(out.sources) > 1 {
		noun = "sources"
	}
	fmt.Printf("playing %s to MIDI %s %s — ctrl-c to stop\n", desc, noun, out.names())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	t := &noteTap{s: s, m: newNoteMapper(p)}
	buf := make([]byte, tapBlock)
	ahead := int64(liveAhead.Seconds() * engine.SampleRate)
	start := time.Now()
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	warned := false
	for {
		select {
		case <-sig:
			fmt.Println()
			return allOff(out, t.m)
		case <-tick.C:
		}
		now := int64(time.Since(start).Seconds() * engine.SampleRate)
		start, now = resync(start, now, s.Frames())
		for s.Frames() < now+ahead {
			t.Read(buf)
		}
		if t.err != nil && !warned {
			fmt.Fprintln(os.Stderr, t.err)
			warned = true
		}
		n := 0
		for _, m := range t.m.out {
			if m.frame > now {
				break
			}
			if err := out.send(m.data); err != nil {
				return err
			}
			n++
		}
		t.m.out = append(t.m.out[:0], t.m.out[n:]...)
	}
}

// maxLag is how far the clock may run ahead of the synth before playback
// counts as stalled rather than late.
const maxLag = engine.SampleRate / 4

// resync handles a stall (ctrl-Z, a debugger, a long pause): the clock has
// run on while nothing rendered. Rendering the gap would send every note
// missed in one burst, and block ctrl-C until it was done, so the clock moves
// up to where the music stopped instead, and it resumes as if paused.
func resync(start time.Time, now, rendered int64) (time.Time, int64) {
	lag := now - rendered
	if lag <= maxLag {
		return start, now
	}
	return start.Add(time.Duration(float64(lag) / engine.SampleRate * float64(time.Second))), rendered
}

// allOff ends the notes still sounding and sends all-notes-off on every
// source, on each channel it carries, so nothing hangs in the receiver.
func allOff(out *liveRoute, m *noteMapper) error {
	for i := range m.held {
		m.release(i, 0)
	}
	for _, msg := range m.out {
		if msg.data[0]&0xf0 == 0x80 {
			out.send(msg.data)
		}
	}
	var err error
	for _, s := range out.sources {
		for _, ch := range s.chans {
			if e := s.sink.send([3]byte{0xb0 | ch, 123, 0}); err == nil {
				err = e
			}
		}
	}
	return err
}
