package letgo

import (
	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
)

// playing is the synth an output is reading, or nil while stopped, and
// whether the output is paused.
func (m *Music) playing() (syn *engine.Synth, paused bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.out == nil {
		return nil, false
	}
	return m.synth, m.paused
}

// LayerLevels is each layer's level, in engine.LayerNames order (see
// engine.Synth.LayerLevels): silent while paused, ok false while stopped.
func (m *Music) LayerLevels() (v [engine.NumLayers]float64, ok bool) {
	syn, paused := m.playing()
	if syn != nil && !paused {
		v = syn.LayerLevels()
	}
	return v, syn != nil
}

// Levels is the mix's band levels: sub, low-mid, mid and high (see
// engine.Synth.Levels): silent while paused, ok false while stopped.
func (m *Music) Levels() (v [4]float64, ok bool) {
	syn, paused := m.playing()
	if syn != nil && !paused {
		v = syn.Levels()
	}
	return v, syn != nil
}

// levelMap is the map for undertone/layer-levels and undertone/levels, or
// nil while stopped.
func levelMap(names []string, v []float64, ok bool) vm.Value {
	if !ok {
		return vm.NIL
	}
	kv := make([]vm.Value, 0, 2*len(names))
	for i, n := range names {
		kv = append(kv, vm.Keyword(n), vm.Float(v[i]))
	}
	return vm.NewPersistentMap(kv)
}

func (m *Music) layerLevels() vm.Value {
	v, ok := m.LayerLevels()
	names := engine.LayerNames()
	return levelMap(names[:], v[:], ok)
}

var bandNames = []string{"sub", "low-mid", "mid", "high"}

func (m *Music) levels() vm.Value {
	v, ok := m.Levels()
	return levelMap(bandNames, v[:], ok)
}
