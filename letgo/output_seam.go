//go:build !js

package letgo

import (
	"io"

	"github.com/nooga/let-go/pkg/rt"
)

// seamOutput plays the synth as one stream in let-go's audio seam
// (nooga/let-go#255), so the music shares the device with the guest's own
// audio/tone and audio/sample sounds. oto allows one context per process, so
// an lg host that opened nativeaudio's player beside the seam's would leave
// one of them silent.
type seamOutput struct {
	a rt.Audio
	h rt.AudioHandle
}

func streamTo(a rt.Audio, src io.Reader) (output, error) {
	h, err := a.Stream(src)
	if err != nil {
		return nil, err
	}
	return &seamOutput{a: a, h: h}, nil
}

// Close starts the mixer's fade-out and returns; the mixer drops the stream
// when the fade ends. Unlike nativeaudio's Close, the synth can still be read
// for those few milliseconds after Close returns. Music.Stop builds a fresh
// synth on the next Start, so nothing reuses the old one.
func (o *seamOutput) Close() { o.a.Stop(o.h) }

// The seam's Pause (nooga/let-go#1115) fades the stream out and then stops
// reading it, so the music's clock holds, as it does when nativeaudio stops
// oto reading; Resume fades it back in from the same place.
func (o *seamOutput) Pause()  { o.a.Pause(o.h) }
func (o *seamOutput) Resume() { o.a.Resume(o.h) }
