//go:build !js

package letgo

import (
	"io"
	"sync/atomic"

	"github.com/nooga/let-go/pkg/rt"
)

// seamOutput plays the synth as one stream in let-go's audio seam
// (nooga/let-go#255), so the music shares the device with the guest's own
// audio/tone and audio/sample sounds. oto allows one context per process, so
// an lg host that opened nativeaudio's player beside the seam's would leave
// one of them silent.
type seamOutput struct {
	a    rt.Audio
	h    rt.AudioHandle
	gate *gate
}

func streamTo(a rt.Audio, src io.Reader) (output, error) {
	g := &gate{src: src}
	h, err := a.Stream(g)
	if err != nil {
		return nil, err
	}
	return &seamOutput{a: a, h: h, gate: g}, nil
}

// Close fades the stream out; the mixer drops it and stops reading the synth.
func (o *seamOutput) Close()  { o.a.Stop(o.h) }
func (o *seamOutput) Pause()  { o.gate.paused.Store(true) }
func (o *seamOutput) Resume() { o.gate.paused.Store(false) }

// gate stands between the mixer and the synth. The seam has no pause, and
// stopping the stream would lose the synth's place, so a paused gate answers
// with silence without reading: the music's clock holds, as it does when
// nativeaudio stops oto reading.
type gate struct {
	src    io.Reader
	paused atomic.Bool
}

func (g *gate) Read(p []byte) (int, error) {
	if g.paused.Load() {
		clear(p)
		return len(p), nil
	}
	return g.src.Read(p)
}
