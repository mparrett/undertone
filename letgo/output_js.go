//go:build js

package letgo

import (
	"io"
	"time"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/webaudio"
)

// In the browser the engine plays through webaudio's pump rather than oto,
// whose js backend crackles on phones (see package webaudio). The queue is 60
// ms, not webaudio's 300, because a game's one-shots render behind it: at 300
// they landed 100-300 ms after the hit. In tesserae (TinyGo 0.43 bridge on the
// page's main thread beside the terminal, 2026-10-26), its audiolab panel
// counted no underruns over minutes of play at 60, 80, 120 and 200 ms, and 8
// at 40 ms; 80 had earlier sounded click-free on an iPhone 13 Pro. On desktop
// Chrome a trigger reached the worklet's output 69 ms after dispatch at 60,
// against 100 ms at 80.
func openOutput(src io.Reader) (output, error) {
	return webaudio.Open(src, webaudio.Options{SampleRate: engine.SampleRate, Queue: 60 * time.Millisecond})
}
