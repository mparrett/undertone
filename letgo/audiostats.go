package letgo

import (
	"time"

	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

// underrunReporter is an output that counts the times its speaker found
// nothing queued: webaudio's. let-go's audio device counts them too but has
// no way to say, and oto doesn't count them.
type underrunReporter interface {
	Underruns() int64
}

// Underruns is how many times the output ran dry since the music started;
// ok is false while stopped, or when the output can't tell.
func (m *Music) Underruns() (n int64, ok bool) {
	m.mu.Lock()
	out := m.out
	m.mu.Unlock()
	if u, isU := out.(underrunReporter); isU {
		return u.Underruns(), true
	}
	return 0, false
}

// RenderStats is the synth's render timings; ok is false while stopped.
func (m *Music) RenderStats() (engine.RenderStats, bool) {
	m.mu.Lock()
	syn, out := m.synth, m.out
	m.mu.Unlock()
	if out == nil {
		return engine.RenderStats{}, false
	}
	return syn.RenderStats(), true
}

// audioStats is undertone/audio-stats: the render timings, and the output's
// underruns and queue where it reports them (keys absent where it doesn't),
// or nil while stopped.
func (m *Music) audioStats() vm.Value {
	m.mu.Lock()
	syn, out := m.synth, m.out
	m.mu.Unlock()
	if out == nil {
		return vm.NIL
	}
	ms := func(d time.Duration) vm.Value { return vm.Float(d.Seconds() * 1000) }
	st := syn.RenderStats()
	kv := []vm.Value{
		vm.Keyword("reads"), vm.Int(st.Reads),
		vm.Keyword("render-ms-last"), ms(st.Last),
		vm.Keyword("render-ms-max"), ms(st.Max),
		vm.Keyword("over"), vm.Int(st.Over),
	}
	if u, ok := out.(underrunReporter); ok {
		kv = append(kv, vm.Keyword("underruns"), vm.Int(u.Underruns()))
	}
	if q, ok := out.(queueReporter); ok {
		kv = append(kv, vm.Keyword("queued-ms"), ms(q.Queued()))
	}
	return vm.NewPersistentMap(kv)
}
