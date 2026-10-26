package engine

import (
	"fmt"
	"math"
	"testing"
)

// peakDBFS renders secs of p and returns its loudest sample in dBFS.
func peakDBFS(p Params, secs int) float64 {
	buf := make([]byte, SampleRate*4*secs)
	NewSynth(p).Read(buf)
	peak := 0.0
	for i := 0; i+1 < len(buf); i += 2 {
		peak = max(peak, math.Abs(float64(int16(uint16(buf[i])|uint16(buf[i+1])<<8))))
	}
	return 20 * math.Log10(peak/32768)
}

type peakCase struct {
	seed    uint64
	level   int
	tension float64
}

// Scenes found loud in past sweeps, kept so the grid below need not land on
// them: village's drums and void's large room.
var loudScenes = map[string][]peakCase{
	"village": {{7, 5, 0}, {2, 5, 1}, {11, 2, 0.8}},
	"void":    {{42, 5, 0}, {7, 5, 0.5}, {42, 2, 0.5}},
}

// Every built-in mood keeps 2 dB of headroom under the master's saturation,
// across low, mid and high levels and tensions. The seed rotates through the
// grid so each mood hears three seeds without rendering every combination.
// Twelve seconds reaches the scenes where the drums, the tension pulse and
// the drone land on one downbeat. -short keeps only full tension, where the
// pulse and tension's drums are loudest.
func TestMoodPeaks(t *testing.T) {
	seeds := []uint64{1, 7, 42}
	levels := []int{0, 5, 9}
	tensions := []float64{0, 0.5, 1}
	if testing.Short() {
		tensions = []float64{1}
	}
	for _, name := range MoodNames() {
		cases := loudScenes[name]
		for i, level := range levels {
			for j, tension := range tensions {
				cases = append(cases, peakCase{seeds[(i+j)%len(seeds)], level, tension})
			}
		}
		for _, c := range cases {
			t.Run(fmt.Sprintf("%s/%d/%d/%v", name, c.seed, c.level, c.tension), func(t *testing.T) {
				t.Parallel()
				p, _ := DeriveMood(name, c.seed, c.level, c.tension)
				if db := peakDBFS(p, 12); db > -2 {
					t.Errorf("peaks at %.2f dBFS, want at most -2", db)
				}
			})
		}
	}
}
