//go:build !js

package letgo

import (
	"encoding/binary"
	"io"
	"testing"

	"github.com/nooga/let-go/pkg/audio"
	"github.com/nooga/let-go/pkg/rt"

	"github.com/mparrett/undertone/engine"
)

// The seam mixes at rt.AudioRate and does not resample, so the synth must
// render at that rate.
func TestSynthRendersAtTheSeamsRate(t *testing.T) {
	if engine.SampleRate != rt.AudioRate {
		t.Fatalf("engine renders at %d Hz, the seam mixes at %d", engine.SampleRate, rt.AudioRate)
	}
}

// level is a source of one constant sample value that counts what it gives.
type level struct {
	v    int16
	read int
}

func (l *level) Read(p []byte) (int, error) {
	for i := 0; i+1 < len(p); i += 2 {
		binary.LittleEndian.PutUint16(p[i:], uint16(l.v))
	}
	l.read += len(p)
	return len(p), nil
}

// mix reads a second of frames and returns the last sample.
func mix(t *testing.T, m *audio.Mixer) int16 {
	t.Helper()
	buf := make([]byte, 4*rt.AudioRate)
	if _, err := m.Read(buf); err != nil {
		t.Fatal(err)
	}
	return int16(binary.LittleEndian.Uint16(buf[len(buf)-2:]))
}

// smooth mixes a second and checks the left channel ends at want without a
// click: no step between frames larger than a 1000-level fade over
// edgeFrames takes.
func smooth(t *testing.T, what string, m *audio.Mixer, want int16) {
	t.Helper()
	buf := make([]byte, 4*rt.AudioRate)
	if _, err := m.Read(buf); err != nil {
		t.Fatal(err)
	}
	const maxStep = 1000/edgeFrames + 1
	prev := int16(binary.LittleEndian.Uint16(buf))
	for i := 4; i+1 < len(buf); i += 4 {
		s := int16(binary.LittleEndian.Uint16(buf[i:]))
		if d := s - prev; d > maxStep || d < -maxStep {
			t.Fatalf("%s: frame %d jumps %d -> %d", what, i/4, prev, s)
		}
		prev = s
	}
	if prev != want {
		t.Fatalf("%s: ends at %d, want %d", what, prev, want)
	}
}

func TestSeamOutputPausesWithoutReadingAndCloses(t *testing.T) {
	m := audio.NewMixer()
	src := &level{v: 1000}
	out, err := streamTo(m, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := mix(t, m); got != 1000 {
		t.Fatalf("playing: last sample %d, want 1000", got)
	}

	out.Pause()
	smooth(t, "pausing", m, 0)
	before := src.read
	if got := mix(t, m); got != 0 {
		t.Fatalf("paused: last sample %d, want silence", got)
	}
	if src.read != before {
		t.Fatalf("paused: the synth was read %d more bytes; its clock must hold", src.read-before)
	}

	out.Resume()
	smooth(t, "resuming", m, 1000)

	out.Close()
	mix(t, m) // the stop's fade
	before = src.read
	if got := mix(t, m); got != 0 {
		t.Fatalf("closed: last sample %d, want silence", got)
	}
	if src.read != before {
		t.Fatalf("closed: the synth is still being read")
	}
}

// A Music started on the seam plays the synth into the mix.
func TestMusicStartsOnTheSeam(t *testing.T) {
	m := audio.NewMixer()
	music := NewMusic(engine.DeriveDungeon(1, 0, 0))
	music.start(func(src io.Reader) (output, error) { return streamTo(m, src) })
	if !music.Started() {
		t.Fatal("music did not start")
	}
	defer music.Stop()
	buf := make([]byte, 4*rt.AudioRate)
	if _, err := m.Read(buf); err != nil {
		t.Fatal(err)
	}
	for _, b := range buf {
		if b != 0 {
			return
		}
	}
	t.Fatal("a second of the started music mixed to silence")
}
