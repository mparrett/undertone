//go:build !js

package letgo

import (
	"encoding/binary"
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
	g := &gate{src: src, level: edgeFrames}
	h, err := a.Stream(g)
	if err != nil {
		return nil, err
	}
	return &seamOutput{a: a, h: h, gate: g}, nil
}

// Close starts the mixer's fade-out and returns; the mixer drops the stream
// when the fade ends. Unlike nativeaudio's Close, the synth can still be read
// for those few milliseconds after Close returns. Music.Stop builds a fresh
// synth on the next Start, so nothing reuses the old one.
func (o *seamOutput) Close()  { o.a.Stop(o.h) }
func (o *seamOutput) Pause()  { o.gate.paused.Store(true) }
func (o *seamOutput) Resume() { o.gate.paused.Store(false) }

// edgeFrames is the pause and resume fade, about 5 ms: the length the seam's
// mixer fades a sound in and out over, so a pause doesn't click either.
const edgeFrames = rt.AudioRate / 200

// gate stands between the mixer and the synth. The seam has no pause, and
// stopping the stream would lose the synth's place, so a paused gate fades
// out and then answers with silence without reading: the music's clock
// holds, as it does when nativeaudio stops oto reading.
type gate struct {
	src    io.Reader
	paused atomic.Bool
	level  int // 0..edgeFrames; read and written only by Read, on the mixer's goroutine
}

func (g *gate) Read(p []byte) (int, error) {
	paused := g.paused.Load()
	if paused && g.level == 0 {
		clear(p)
		return len(p), nil
	}
	n, err := g.src.Read(p)
	if !paused && g.level == edgeFrames {
		return n, err
	}
	// Mid-fade: step the level one frame at a time toward the target.
	for i := 0; i+4 <= n; i += 4 {
		if paused && g.level > 0 {
			g.level--
		} else if !paused && g.level < edgeFrames {
			g.level++
		}
		for c := i; c < i+4; c += 2 {
			s := int32(int16(binary.LittleEndian.Uint16(p[c:])))
			binary.LittleEndian.PutUint16(p[c:], uint16(int16(s*int32(g.level)/edgeFrames)))
		}
	}
	return n, err
}
