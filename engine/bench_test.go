package engine

import (
	"fmt"
	"testing"
	"time"
)

// benchSlice is the frames rendered per Read: the browser pump's slice.
const benchSlice = 512

// benchRender times b.N slices of s, calling each before its slice, and
// reports the speed as a multiple of real time, which is the number that
// says whether a device keeps up: the browser pump needs a little over 1x
// to hold its queue (see webaudio's pump).
func benchRender(b *testing.B, s *Synth, each func(i int)) {
	buf := make([]byte, benchSlice*4)
	for i := 0; i < SampleRate/benchSlice; i++ { // a second in, past the first notes' allocations
		s.Read(buf)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if each != nil {
			each(i)
		}
		s.Read(buf)
	}
	audio := time.Duration(b.N) * benchSlice * time.Second / SampleRate
	b.ReportMetric(float64(audio)/float64(b.Elapsed()), "x-realtime")
}

// BenchmarkMood renders each built-in mood at rest and at full tension.
// Run it natively with `just bench` and as wasm, which is what a browser
// runs and several times slower, with `just bench-wasm`.
func BenchmarkMood(b *testing.B) {
	for _, name := range MoodNames() {
		for _, tension := range []float64{0, 1} {
			b.Run(fmt.Sprintf("%s/tension=%v", name, tension), func(b *testing.B) {
				p, _ := DeriveMood(name, 7, 4, tension)
				benchRender(b, NewSynth(p), nil)
			})
		}
	}
}

// BenchmarkShots is a mood under a one-shot about every 100 ms, cycling
// through the built-ins: a busy fight. The shots cost several times the
// music, so this is the case to watch on a slow device.
func BenchmarkShots(b *testing.B) {
	names := ShotNames()
	for _, name := range []string{"dungeon", "village", "forge"} {
		b.Run(name, func(b *testing.B) {
			p, _ := DeriveMood(name, 7, 4, 1)
			s := NewSynth(p)
			benchRender(b, s, func(i int) {
				if i%9 == 0 { // 9 slices of 512 frames: 104 ms
					s.Trigger(names[i/9%len(names)], 60, 1, 0.5, 0, uint64(i))
				}
			})
		})
	}
}

// Once a mood is playing, rendering it allocates nothing: the audio thread
// must not wait on the allocator or bring on a collection, which in a
// browser stops the one thread there is. What a voice needs is allocated at
// its first note (its unit states, a string's line), so the count is taken
// after every track has played. The render is deterministic, so a count
// above zero here is a new allocation in the render path, not noise.
func TestSteadyRenderDoesNotAllocate(t *testing.T) {
	for _, name := range MoodNames() {
		for _, tension := range []float64{0, 1} {
			t.Run(fmt.Sprintf("%s/tension=%v", name, tension), func(t *testing.T) {
				p, _ := DeriveMood(name, 7, 4, tension)
				s := NewSynth(p)
				// two patterns: AllocsPerRun plays this once to warm up,
				// then counts over a second pass
				buf := make([]byte, 2*rows*s.samplesPerRow*4)
				if n := testing.AllocsPerRun(1, func() { s.Read(buf) }); n != 0 {
					t.Errorf("%v allocations in two patterns of steady play, want 0", n)
				}
			})
		}
	}
}
