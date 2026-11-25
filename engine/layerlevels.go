package engine

import (
	"math"
	"sync/atomic"
)

// NumLayers is how many layers there are: the seven tracks and the drums, in
// Layers bit order. The drums are not last: the bass, added later, is.
const NumLayers = 8

// DrumsLayer is the drums' index among the layers.
const DrumsLayer = 6

const layerDrums = DrumsLayer

// LayerNames names the layers in Layers bit order: the index LayerLevels
// uses. The fourth is the lead, whichever instrument holds the slot.
func LayerNames() [NumLayers]string {
	return [NumLayers]string{"drone", "fifth", "pad", "lead", "air", "pulse", "drums", "bass"}
}

// layerMeter is a peak meter per layer. The frame loop takes each layer's
// loudest dry sample, and every layerFold frames the envelopes decay over
// the block and take its peaks: decaying per sample, as the mix meter does,
// costs a multiply per layer per frame, and folding only once per Read
// would publish a peak from the start of a long Read (a native buffer, a
// WAV render) as if it had just happened.
//
// One more slot past the layers meters the echo: the delay's return, which
// every layer feeds and so none can own.
type layerMeter struct {
	peak [NumLayers + 1]float64
	env  [NumLayers + 1]float64
	out  [NumLayers + 1]atomic.Uint64 // env as float bits, for other goroutines
}

// layerEcho is the echo's slot in layerMeter, after the layers.
const layerEcho = NumLayers

// layerFold bounds how late a peak is decayed: by 256 frames, a peak reads
// at most 7% above where a per-sample envelope would be. The frame loop folds
// on its own frame count; a power of two makes that a mask.
const layerFold = 256

var layerFoldDecay = math.Pow(mdecay, layerFold)

// see notes layer i's dry output this frame: what its voices added to the
// mono sum. Before the music gain and whether or not the layer is muted, so
// a muted layer still shows that it plays.
func (m *layerMeter) see(i int, added float64) {
	m.peak[i] = max(m.peak[i], math.Abs(added)/2)
}

// fold decays the envelopes by d, the decay over the frames since the last
// fold, and takes those frames' peaks.
func (m *layerMeter) fold(d float64) {
	for i := range m.env {
		m.env[i] = max(m.peak[i], flush(m.env[i]*d))
		m.peak[i] = 0
	}
}

// publish makes the envelopes readable from other goroutines, as they
// stand since frames into the current block. It leaves env alone: the
// block's own fold decays it over the whole block.
func (m *layerMeter) publish(since int) {
	d := math.Pow(mdecay, float64(since))
	for i := range m.env {
		m.out[i].Store(math.Float64bits(max(m.peak[i], flush(m.env[i]*d))))
	}
}

// LayerLevels returns each layer's level as of the end of the last Read,
// in LayerNames order: a peak envelope of the layer's dry output, before
// the music gain and the delay and reverb, and whether or not it is muted.
// Safe to call from any goroutine.
//
// The echo is not among them: see EchoLevel.
func (s *Synth) LayerLevels() [NumLayers]float64 {
	var v [NumLayers]float64
	for i := range v {
		v[i] = math.Float64frombits(s.layers.out[i].Load())
	}
	return v
}

// EchoLevel returns the echo's level as of the end of the last Read: a peak
// envelope, on LayerLevels' scale, of what the shared delay returns to the
// mix. Every layer feeds the delay, and so do one-shots played with a delay
// send, so this is all of their echoes at once.
// Unlike LayerLevels it comes after the music gain, which scales what goes
// into the delay. Safe to call from any goroutine.
func (s *Synth) EchoLevel() float64 {
	return math.Float64frombits(s.layers.out[layerEcho].Load())
}
