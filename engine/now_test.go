package engine

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// A mood staged with SetParametersNow is heard within one block of the call,
// mid-pattern or just after a pattern's last row has stepped, and the drone
// has moved to the new mood's patch once the fades are done.
func TestMoodChangeHeardWithinABlock(t *testing.T) {
	dungeon, _ := DeriveMood("dungeon", 7, 2, 0)
	rust, _ := DeriveMood("rust", 7, 2, 0)
	for _, c := range []struct {
		name string
		rows int // rows stepped before the change
	}{{"mid-pattern", 5}, {"after the last row", rows}} {
		a, b := NewSynth(dungeon), NewSynth(dungeon)
		lead := (c.rows-1)*a.samplesPerRow + 1 // just past the row's step
		render(a, lead)
		render(b, lead)
		a.SetParametersNow(rust)
		switched, kept := render(a, nowBlock), render(b, nowBlock)
		if bytes.Equal(switched, kept) {
			t.Errorf("%s: no change heard within %d frames", c.name, nowBlock)
		}
		render(a, 2*fadeFrames+1)
		if v := &a.voices[0]; !samePatch(v.patch, rust.Palette.Drone) || v.gain != v.gainTo {
			t.Errorf("%s: after the fades the drone is not rust's at full gain", c.name)
		}
	}
}

// The switch fades instead of cutting: the largest sample-to-sample step
// across it stays near the largest a steady render takes.
func TestMoodChangeDoesNotClick(t *testing.T) {
	dungeon, _ := DeriveMood("dungeon", 7, 2, 0)
	rust, _ := DeriveMood("rust", 7, 2, 0)
	s := NewSynth(dungeon)
	steady := maxStep(render(s, 5*s.samplesPerRow))
	s.SetParametersNow(rust)
	if got := maxStep(render(s, nowBlock+4*fadeFrames)); got > 2*steady {
		t.Errorf("largest step across the switch is %d, against %d steady", got, steady)
	}
}

// Re-staging the params already playing redraws the same notes, so it
// changes nothing: an editor may send the same params on every keystroke.
func TestRestagingSameParamsChangesNothing(t *testing.T) {
	p, _ := DeriveMood("village", 3, 4, 0.5)
	a, b := NewSynth(p), NewSynth(p)
	for range 40 {
		a.SetParametersNow(p)
		if !bytes.Equal(render(a, 3001), render(b, 3001)) {
			t.Fatal("re-staging the same params changed the render")
		}
	}
}

func maxStep(pcm []byte) int {
	best, prev := 0, int(int16(binary.LittleEndian.Uint16(pcm)))
	for i := 4; i < len(pcm); i += 4 {
		x := int(int16(binary.LittleEndian.Uint16(pcm[i:])))
		best = max(best, abs(x-prev))
		prev = x
	}
	return best
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
