//go:build !js

package main

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mparrett/undertone/engine"
)

// fakeSink records what a source was sent, in place of CoreMIDI.
type fakeSink struct {
	name   string
	sent   [][3]byte
	closed bool
}

func (f *fakeSink) send(d [3]byte) error { f.sent = append(f.sent, d); return nil }
func (f *fakeSink) close()               { f.closed = true }

// fakeOpener opens fakeSinks, failing on the name fail.
func fakeOpener(fail string) (func(string) (midiSink, error), *[]*fakeSink) {
	var opened []*fakeSink
	return func(name string) (midiSink, error) {
		if name == fail {
			return nil, errors.New("no")
		}
		f := &fakeSink{name: name}
		opened = append(opened, f)
		return f, nil
	}, &opened
}

func allLayers() (l [engine.NumLayers]bool) {
	for i := range l {
		l[i] = true
	}
	return l
}

// noteOn is a layer's note on, on its channel.
func noteOn(layer int) [3]byte { return [3]byte{0x90 | layerChannel(layer), 60, 100} }

// Without -midi-split every layer goes to one "undertone" source on its own
// channel, and all-notes-off covers channels 1 to 8 and 10, as before split.
func TestRouteSingleSource(t *testing.T) {
	open, opened := fakeOpener("")
	r, err := openRoute(false, allLayers(), open)
	if err != nil {
		t.Fatal(err)
	}
	if len(*opened) != 1 || (*opened)[0].name != "undertone" {
		t.Fatalf("opened %v, want one source \"undertone\"", *opened)
	}
	src := (*opened)[0]
	var want [][3]byte
	for l := range engine.NumLayers {
		r.send(noteOn(l))
		want = append(want, noteOn(l))
	}
	r.send([3]byte{0x96, 60, 100}) // channel 7: no layer's
	if !slices.Equal(src.sent, want) {
		t.Errorf("sent %v, want %v", src.sent, want)
	}

	src.sent = nil
	if err := allOff(r, &noteMapper{}); err != nil {
		t.Fatal(err)
	}
	want = nil
	for _, ch := range []byte{0, 1, 2, 3, 4, 5, 6, 7, drumChannel} {
		want = append(want, [3]byte{0xb0 | ch, 123, 0})
	}
	if !slices.Equal(src.sent, want) {
		t.Errorf("all-notes-off %v, want %v", src.sent, want)
	}
}

// -layer without split filters the single source by channel.
func TestRouteSingleSourceLayers(t *testing.T) {
	open, opened := fakeOpener("")
	var l [engine.NumLayers]bool
	l[3], l[engine.DrumsLayer] = true, true
	r, err := openRoute(false, l, open)
	if err != nil {
		t.Fatal(err)
	}
	for i := range engine.NumLayers {
		r.send(noteOn(i))
	}
	want := [][3]byte{noteOn(3), noteOn(engine.DrumsLayer)}
	if got := (*opened)[0].sent; !slices.Equal(got, want) {
		t.Errorf("sent %v, want %v", got, want)
	}
}

// With split each layer has a source named for it and gets its own notes
// only, on the channel it has without split: the drums stay on 10.
func TestRouteSplit(t *testing.T) {
	open, opened := fakeOpener("")
	r, err := openRoute(true, allLayers(), open)
	if err != nil {
		t.Fatal(err)
	}
	names := engine.LayerNames()
	if len(*opened) != engine.NumLayers {
		t.Fatalf("opened %d sources, want %d", len(*opened), engine.NumLayers)
	}
	for i, f := range *opened {
		if want := "undertone " + names[i]; f.name != want {
			t.Errorf("source %d is %q, want %q", i, f.name, want)
		}
	}
	for l := range engine.NumLayers {
		r.send(noteOn(l))
	}
	for l, f := range *opened {
		if want := [][3]byte{noteOn(l)}; !slices.Equal(f.sent, want) {
			t.Errorf("%s got %v, want %v", f.name, f.sent, want)
		}
	}
	if got := (*opened)[engine.DrumsLayer].sent[0][0] & 0x0f; got != drumChannel {
		t.Errorf("drums on channel %d, want %d", got+1, drumChannel+1)
	}
}

// With split, -layer opens only the named layers' sources.
func TestRouteSplitLayers(t *testing.T) {
	open, opened := fakeOpener("")
	var l [engine.NumLayers]bool
	l[0], l[engine.DrumsLayer] = true, true
	r, err := openRoute(true, l, open)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range *opened {
		got = append(got, f.name)
	}
	if want := []string{"undertone drone", "undertone drums"}; !slices.Equal(got, want) {
		t.Fatalf("opened %v, want %v", got, want)
	}
	for i := range engine.NumLayers {
		r.send(noteOn(i))
	}
	if s := (*opened)[0].sent; !slices.Equal(s, [][3]byte{noteOn(0)}) {
		t.Errorf("drone got %v", s)
	}
	if s := (*opened)[1].sent; !slices.Equal(s, [][3]byte{noteOn(engine.DrumsLayer)}) {
		t.Errorf("drums got %v", s)
	}
}

// A source that fails to open closes the ones opened before it.
func TestRouteSplitOpenFails(t *testing.T) {
	open, opened := fakeOpener("undertone lead")
	if _, err := openRoute(true, allLayers(), open); err == nil {
		t.Fatal("no error")
	}
	if len(*opened) != 3 {
		t.Fatalf("opened %d before the lead, want 3", len(*opened))
	}
	for _, f := range *opened {
		if !f.closed {
			t.Errorf("%s left open", f.name)
		}
	}
}

// At the end each split source gets the note-offs for its own held notes and
// all-notes-off on its channel.
func TestAllOffSplit(t *testing.T) {
	open, opened := fakeOpener("")
	r, err := openRoute(true, allLayers(), open)
	if err != nil {
		t.Fatal(err)
	}
	m := &noteMapper{}
	m.held[3] = held{on: true, ch: 3, note: 64}
	m.held[engine.NumLayers] = held{on: true, ch: drumChannel, note: gmDrum[0]}
	if err := allOff(r, m); err != nil {
		t.Fatal(err)
	}
	for l, f := range *opened {
		var want [][3]byte
		switch l {
		case 3:
			want = append(want, [3]byte{0x83, 64, 0})
		case engine.DrumsLayer:
			want = append(want, [3]byte{0x80 | drumChannel, gmDrum[0], 0})
		}
		want = append(want, [3]byte{0xb0 | layerChannel(l), 123, 0})
		if !slices.Equal(f.sent, want) {
			t.Errorf("%s got %v, want %v", f.name, f.sent, want)
		}
	}
}

// After a stall the clock moves up to the synth rather than the synth racing
// to the clock, so the missed notes are not sent in one burst.
func TestResyncAfterAStall(t *testing.T) {
	start := time.Unix(1000, 0)
	if s, now := resync(start, 5000, 4000); !s.Equal(start) || now != 5000 {
		t.Errorf("a lag under maxLag moved the clock: %v %d", s, now)
	}
	lag := int64(300 * engine.SampleRate) // five minutes stopped
	s, now := resync(start, 1000+lag, 1000)
	if now != 1000 {
		t.Errorf("now %d after a stall, want the synth's 1000", now)
	}
	if got := s.Sub(start); got != 300*time.Second {
		t.Errorf("clock moved %v, want the stall's 5m0s", got)
	}
}
