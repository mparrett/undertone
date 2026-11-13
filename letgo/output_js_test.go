//go:build js

package letgo

import (
	"io"
	"syscall/js"
	"testing"
	"time"

	"github.com/nooga/let-go/pkg/rt"
)

// fakeAudio is a backend that reports whether it can play.
type fakeAudio struct{ enabled bool }

func (fakeAudio) Play(rt.Sound) (rt.AudioHandle, error)       { return 1, nil }
func (fakeAudio) Stream(io.Reader) (rt.AudioHandle, error)    { return 1, nil }
func (fakeAudio) Stop(rt.AudioHandle)                         {}
func (fakeAudio) Pause(rt.AudioHandle)                        {}
func (fakeAudio) Resume(rt.AudioHandle)                       {}
func (fakeAudio) Gain(rt.AudioHandle, float64, time.Duration) {}
func (f fakeAudio) Enabled() bool                             { return f.enabled }

// withHost defines _lgAudioOpen, as every lg -w bundle's lg-host-core.js does,
// and binds *audio* to a, restoring both afterwards.
func withHost(t *testing.T, a rt.Audio) {
	t.Helper()
	g := js.Global()
	f := js.FuncOf(func(js.Value, []js.Value) any { return nil })
	g.Set("_lgAudioOpen", f)
	v := rt.LookupCoreVar("*audio*")
	old := v.Root()
	if a != nil {
		rt.SetAudioRoot(a)
	}
	t.Cleanup(func() {
		g.Delete("_lgAudioOpen")
		f.Release()
		v.SetRoot(old)
	})
}

// A bundle built without -w-audio still has _lgAudioOpen, and *audio* is its
// default no-op: the music must keep webaudio, not stream into silence.
func TestSeamDeviceSkipsTheDefaultNoOp(t *testing.T) {
	withHost(t, nil)
	if _, ok := seamDevice(); ok {
		t.Fatal("took the default no-op *audio* for let-go's browser device")
	}
}

func TestSeamDeviceSkipsADeviceThatCannotPlay(t *testing.T) {
	withHost(t, fakeAudio{enabled: false})
	if _, ok := seamDevice(); ok {
		t.Fatal("chose a device that failed to open")
	}
}

func TestSeamDeviceTakesAnOpenDevice(t *testing.T) {
	withHost(t, fakeAudio{enabled: true})
	if _, ok := seamDevice(); !ok {
		t.Fatal("passed over an open device in a -w-audio bundle")
	}
}

// undertone's own example pages have no lg host at all.
func TestSeamDeviceNeedsTheLgHost(t *testing.T) {
	v := rt.LookupCoreVar("*audio*")
	old := v.Root()
	rt.SetAudioRoot(fakeAudio{enabled: true})
	t.Cleanup(func() { v.SetRoot(old) })
	if _, ok := seamDevice(); ok {
		t.Fatal("chose the seam on a page without _lgAudioOpen")
	}
}
