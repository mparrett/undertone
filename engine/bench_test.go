package engine

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// benchSlice is the frames rendered per Read: the browser pump's slice.
const benchSlice = 512

// builtinShots is the built-in one-shots, sorted: ShotNames without what
// other tests in the binary have registered.
func builtinShots() []string {
	names := make([]string, 0, len(shotLib))
	for n := range shotLib {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// benchRender times b.N slices of s, calling each before its slice, and
// reports the speed as a multiple of real time, which is the number that
// says whether a device keeps up: the browser pump needs a little over 1x
// to hold its queue (see webaudio's pump).
func benchRender(b *testing.B, s *Synth, each func(i int)) {
	buf := make([]byte, benchSlice*4)
	for i := 0; i < SampleRate/benchSlice; i++ { // a second in, so the mood is sounding
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
// music, so this is the case to watch on a slow device. Its allocations are
// Trigger's, on the caller's side.
func BenchmarkShots(b *testing.B) {
	names := builtinShots()
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

// readAllocs counts the objects allocated so far by calls that came through
// Synth.Read, from the heap profile. Counting by call stack leaves out what
// the rest of the process allocates meanwhile (the test runner, other
// goroutines), which a count of all allocations, as testing.AllocsPerRun
// makes, would hold against the render. It needs runtime.MemProfileRate at 1.
func readAllocs() int64 {
	// the profile shows allocations as of the last collection but one
	runtime.GC()
	runtime.GC()
	var recs []runtime.MemProfileRecord
	for {
		n, ok := runtime.MemProfile(recs, true)
		if ok {
			recs = recs[:n]
			break
		}
		recs = make([]runtime.MemProfileRecord, n+64)
	}
	var total int64
	for i := range recs {
		frames := runtime.CallersFrames(recs[i].Stack())
		for {
			f, more := frames.Next()
			if strings.HasSuffix(f.Function, "engine.(*Synth).Read") {
				total += recs[i].AllocObjects
				break
			}
			if !more {
				break
			}
		}
	}
	return total
}

// Rendering allocates nothing: the audio thread must not wait on the
// allocator or bring on a collection, which in a browser stops the one
// thread there is. What the voices need (unit states, a string's line) comes
// with the synth, so this holds from the first notes, through every mood,
// and under every built-in one-shot.
//
// A change of parameters is the exception: applying one publishes a copy for
// other goroutines to read, and a tempo slower than any yet played lengthens
// the delay line. Those happen when a game asks, not as the music plays.
func TestRenderDoesNotAllocate(t *testing.T) {
	old := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = old }()

	shots := builtinShots()
	var synths []*Synth
	for _, name := range MoodNames() {
		for _, tension := range []float64{0, 1} {
			p, _ := DeriveMood(name, 7, 4, tension)
			synths = append(synths, NewSynth(p))
		}
	}
	buf := make([]byte, benchSlice*4)
	before := readAllocs()
	for _, s := range synths {
		slices := 2 * rows * s.samplesPerRow / benchSlice // two patterns
		for j := 0; j < slices; j++ {
			if j%9 == 0 {
				s.Trigger(shots[j/9%len(shots)], 30+j%60, 1, 0.5, 0, uint64(j))
			}
			s.Read(buf)
		}
	}
	if n := readAllocs() - before; n != 0 {
		t.Errorf("%d allocations inside Synth.Read, want 0", n)
	}
}
