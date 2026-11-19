package letgo

import (
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

// readingOutput reads its source on its own goroutine until closed, as a
// device's callback does.
type readingOutput struct {
	closed atomic.Bool
	done   chan struct{}
}

func openReading(src io.Reader) (output, error) {
	o := &readingOutput{done: make(chan struct{})}
	go func() {
		defer close(o.done)
		buf := make([]byte, 512*4)
		for !o.closed.Load() {
			src.Read(buf)
			runtime.Gosched() // see the note on the loops below
		}
	}()
	return o, nil
}

func (o *readingOutput) Close()  { o.closed.Store(true); <-o.done }
func (o *readingOutput) Pause()  {}
func (o *readingOutput) Resume() {}

// The loops here yield on every pass because js/wasm runs every goroutine on
// one thread and switches only when one blocks: a loop that never blocks
// spins forever there, and the test never ends. Natively the yields cost
// next to nothing, and under js/wasm they give real interleavings to test.

func kwMap(kv ...vm.Value) vm.Value { return vm.NewPersistentMap(kv) }

// The natives' side of a Music called from several goroutines while an
// output reads the synth, through a stop and a second start: a game thread,
// a page's handlers and the audio callback. Like the engine's test of the
// same name, it is there for the race detector (`just race`).
func TestConcurrentCallsWhilePlaying(t *testing.T) {
	m := NewMusic(engine.DeriveDungeon(7, 2, 0))
	kw := func(s string) vm.Value { return vm.Keyword(s) }
	moods := engine.MoodNames()
	th := kwMap(kw("grids"), vm.NewArrayVector([]vm.Value{
		kwMap(kw("lead"), vm.NewArrayVector([]vm.Value{vm.Int(1), vm.Int(3), vm.Int(5)}), kw("bed"), vm.TRUE),
	}), kw("then"), kw("loop"))

	controls := []func(i int){
		func(i int) {
			m.setParameters(kwMap(kw("mood"), kw(moods[i%len(moods)]), kw("bpm"), vm.Float(60+float64(i%40))), i%2 == 0)
		},
		func(i int) {
			m.trigger(vm.NewArrayVector([]vm.Value{
				kwMap(kw("shot"), kw("hit"), kw("degree"), vm.Int(i%8)),
				kwMap(kw("shot"), kw("footstep"), kw("gain"), vm.Float(0.5)),
			}))
		},
		func(i int) {
			m.setGain(musicBus, float64(i%5)/4)
			m.glideGain(sfxBus, float64(i%3)/2, 0.01)
		},
		func(i int) {
			if i%3 == 0 {
				m.playTheme(vm.NIL)
			} else {
				m.playTheme(th)
			}
			m.themePlaying()
		},
		func(i int) {
			m.Params()
			m.Started()
			m.position()
			m.audioStats()
			m.layerLevels()
			m.levels()
			m.noteEvents(vm.Int(i))
			m.Pause()
			m.Resume()
		},
		func(i int) { m.SetParams(engine.DeriveOverworld(uint64(i), i%6, 0.5)) },
	}

	var stop atomic.Bool
	var wg sync.WaitGroup
	for _, control := range controls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; !stop.Load(); i++ {
				control(i)
				runtime.Gosched()
			}
		}()
	}
	for range 20 {
		m.start(openReading)
		for f := int64(0); f < engine.SampleRate/4; { // a quarter second played
			m.mu.Lock()
			if m.synth != nil {
				f = m.synth.Frames()
			}
			m.mu.Unlock()
			runtime.Gosched()
		}
		m.Stop()
	}
	stop.Store(true)
	wg.Wait()
}
