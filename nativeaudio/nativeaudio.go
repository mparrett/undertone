//go:build !js

// Package nativeaudio plays an io.Reader of interleaved stereo signed 16-bit
// little-endian frames through oto on desktop: the native counterpart of
// package webaudio, with the same Open, Pause, Resume and Close.
//
// It owns oto's rules so callers need not: one context per process, a player
// handle kept reachable while it plays (oto#293), and PauseAndStopReading to
// stop it.
package nativeaudio

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
)

// Options configures the output. Zero values take the defaults in brackets.
type Options struct {
	SampleRate int           // [44100] of the source; the first Open in a process fixes it
	Queue      time.Duration // [24 ms on macOS, else 40 ms; at least one device buffer] read-ahead: shorter hears a trigger sooner, longer survives stalls
}

// oto allows one context per process, so it is opened once and shared: a
// player that is closed and opened again reuses it rather than failing.
var (
	ctxOnce  sync.Once
	ctx      *oto.Context
	ctxReady chan struct{}
	ctxErr   error
	ctxRate  int
)

// Player streams a source to the default output device.
type Player struct {
	// Kept in a field: oto closes a player once its handle is unreachable,
	// even mid-playback, silently (oto#293).
	player *oto.Player
}

// Open starts playing src and returns once the device is ready.
func Open(src io.Reader, opt Options) (*Player, error) {
	if opt.SampleRate == 0 {
		opt.SampleRate = 44100
	}
	queue, frames, err := overrides(opt.SampleRate, opt.Queue)
	if err != nil {
		return nil, err
	}
	opt.Queue = queue
	ctxOnce.Do(func() {
		ctxRate = opt.SampleRate
		ctx, ctxReady, ctxErr = oto.NewContext(&oto.NewContextOptions{
			SampleRate:   opt.SampleRate,
			ChannelCount: 2,
			Format:       oto.FormatSignedInt16LE,
			BufferSize:   deviceBuffer(opt.SampleRate, frames),
		})
	})
	if ctxErr != nil {
		return nil, ctxErr
	}
	if opt.SampleRate != ctxRate {
		return nil, fmt.Errorf("nativeaudio: the output is open at %d Hz, not %d", ctxRate, opt.SampleRate)
	}
	<-ctxReady
	p := &Player{}
	p.player = ctx.NewPlayer(src)
	// Before Play: oto's default read-ahead is half a second, which a
	// one-shot has to wait out (see playerBuffer).
	p.player.SetBufferSize(playerBufferBytes(opt.SampleRate, opt.Queue))
	p.player.Play()
	return p, nil
}

func (p *Player) Pause()  { p.player.Pause() }
func (p *Player) Resume() { p.player.Play() }

// Close stops the player and waits out any read in flight, so the source is
// not read after Close returns. Player.Close is a deprecated no-op since oto
// v3.4; PauseAndStopReading is what takes the player out of oto's mixer.
func (p *Player) Close() { p.player.PauseAndStopReading() }
