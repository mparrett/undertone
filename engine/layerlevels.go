package engine

import (
	"math"
	"sync/atomic"
)

// NumLayers is how many layers there are: the six tracks and the drums, in
// Layers bit order.
const NumLayers = 7

const layerDrums = NumLayers - 1

// LayerNames names the layers in Layers bit order: the index LayerLevels
// uses. The fourth is the lead, whichever instrument holds the slot.
func LayerNames() [NumLayers]string {
	return [NumLayers]string{"drone", "fifth", "pad", "lead", "air", "pulse", "drums"}
}

// layerMeter is a peak meter per layer. The frame loop takes each layer's
// loudest dry sample over a Read; the decay runs once per Read, not per
// sample, which a meter drawn a few dozen times a second cannot tell apart.
type layerMeter struct {
	peak [NumLayers]float64
	env  [NumLayers]float64
	out  [NumLayers]atomic.Uint64 // env as float bits, for other goroutines
}

// see notes layer i's dry output this frame: what its voices added to the
// mono sum. Before the music gain and whether or not the layer is muted, so
// a muted layer still shows that it plays.
func (m *layerMeter) see(i int, added float64) {
	m.peak[i] = max(m.peak[i], math.Abs(added)/2)
}

// publish decays the envelopes over frames, takes this Read's peaks and
// makes them readable from other goroutines.
func (m *layerMeter) publish(frames int) {
	d := math.Pow(mdecay, float64(frames))
	for i := range m.env {
		m.env[i] = max(m.peak[i], flush(m.env[i]*d))
		m.peak[i] = 0
		m.out[i].Store(math.Float64bits(m.env[i]))
	}
}

// LayerLevels returns each layer's level as of the end of the last Read,
// in LayerNames order: a peak envelope of the layer's dry output, before
// the music gain and the delay and reverb, and whether or not it is muted.
// Safe to call from any goroutine.
func (s *Synth) LayerLevels() [NumLayers]float64 {
	var v [NumLayers]float64
	for i := range v {
		v[i] = math.Float64frombits(s.layers.out[i].Load())
	}
	return v
}
