package letgo

import (
	"time"

	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

// underrunReporter is an output that counts the 128-frame quanta its
// speaker had to pad with silence for want of audio: webaudio's. let-go's
// audio device counts them too but has no way to say, and oto doesn't count
// them.
type underrunReporter interface {
	Underruns() int64
}

// Underruns is how many 128-frame quanta (2.9 ms each) the output has had
// to fill with silence since playback began: one 12 ms stall counts about
// 4. ok is false while stopped, or when the output can't tell.
func (m *Music) Underruns() (n int64, ok bool) {
	m.mu.Lock()
	out := m.out
	m.mu.Unlock()
	if u, isU := out.(underrunReporter); isU {
		return u.Underruns(), true
	}
	return 0, false
}

// queued is how much audio the output holds ahead of the speaker; ok is
// false while stopped, or when the output can't tell.
func (m *Music) queued() (time.Duration, bool) {
	m.mu.Lock()
	out := m.out
	m.mu.Unlock()
	if q, isQ := out.(queueReporter); isQ {
		return q.Queued(), true
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
	st, ok := m.RenderStats()
	if !ok {
		return vm.NIL
	}
	ms := func(d time.Duration) vm.Value { return vm.Float(d.Seconds() * 1000) }
	var mean time.Duration
	if st.Reads > 0 {
		mean = st.Total / time.Duration(st.Reads)
	}
	kv := []vm.Value{
		vm.Keyword("reads"), vm.Int(st.Reads),
		vm.Keyword("render-ms-last"), ms(st.Last),
		vm.Keyword("render-ms-mean"), ms(mean),
		vm.Keyword("render-ms-max"), ms(st.Max),
		vm.Keyword("over"), vm.Int(st.Over),
	}
	if n, ok := m.Underruns(); ok {
		kv = append(kv, vm.Keyword("underruns"), vm.Int(n))
	}
	if d, ok := m.queued(); ok {
		kv = append(kv, vm.Keyword("queued-ms"), ms(d))
	}
	return vm.NewPersistentMap(kv)
}
