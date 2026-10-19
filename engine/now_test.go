package engine

import (
	"bytes"
	"encoding/binary"
	"math"
	"slices"
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
// across it stays near the largest either mood takes playing steadily.
func TestMoodChangeDoesNotClick(t *testing.T) {
	dungeon, _ := DeriveMood("dungeon", 7, 2, 0)
	rust, _ := DeriveMood("rust", 7, 2, 0)
	steady := max(steadyStep(dungeon), steadyStep(rust))
	s := NewSynth(dungeon)
	render(s, 5*s.samplesPerRow)
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

// steadyStep is the largest sample-to-sample step p takes in its first
// seconds: saws step sharply every cycle, so this differs by mood.
func steadyStep(p Params) int {
	return maxStep(render(NewSynth(p), 6*SampleRate))
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

// A note waiting on a fade still starts when the old note dies first, as a
// release tail can partway through the fade.
func TestPendingNoteSurvivesTheOldNoteDying(t *testing.T) {
	var v Voice
	v.trigger(pulse, 40, 1, 0.5, 1)
	v.gate = false
	probe := v
	probe.units = slices.Clone(v.units)
	left := 0
	for ; probe.active; left++ {
		var l, r float64
		probe.run(&l, &r, &sends{})
	}
	run := func(n int) {
		for range n {
			if v.active {
				var l, r float64
				v.run(&l, &r, &sends{})
			}
		}
	}
	run(left - 5)
	v.retrigger(trackRow{45, 0.6, 16, drone}, 16, 2)
	run(2 * fadeFrames)
	if !v.active || !samePatch(v.patch, drone) || v.gain != 0.6 {
		t.Errorf("the pending drone did not start: active %v, gain %v", v.active, v.gain)
	}
}

// A switch whose fade straddles a row step keeps fading: the row's new note
// joins the fade, where a plain trigger would step the voice's gain.
func TestSwitchAcrossARowKeepsFading(t *testing.T) {
	dungeon, _ := DeriveMood("dungeon", 7, 2, 0)
	rust, _ := DeriveMood("rust", 7, 2, 0)
	s := NewSynth(dungeon)
	edge := rows * s.samplesPerRow // the next pattern's first row, with its drone
	lead := (edge - 100) / nowBlock * nowBlock
	render(s, lead)
	s.SetParametersNow(rust)
	var was [nTracks]float64
	for i := range s.voices {
		was[i] = s.voices[i].gain
	}
	for range 2*(edge-lead) + 4*fadeFrames {
		render(s, 1)
		for i := range s.voices {
			v := &s.voices[i]
			if d := v.gain - was[i]; v.active && math.Abs(d) > 1.0/fadeFrames {
				t.Fatalf("track %d's gain stepped by %v in one frame", i, d)
			}
			was[i] = v.gain
		}
	}
}
