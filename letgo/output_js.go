//go:build js

package letgo

import (
	"io"
	"time"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/webaudio"
)

// In the browser the engine plays through webaudio's pump rather than oto,
// whose js backend crackles on phones (see package webaudio). The queue is 80
// ms, not webaudio's 300, because a game's one-shots render behind it: at 300
// they landed 100-300 ms after the hit. 80 sounded tighter than 140 and 300,
// with no clicks heard, in tesserae on an iPhone 13 Pro (TinyGo 0.43 bridge on
// the page's main thread beside the terminal, 2026-10-26). That is ear
// evidence, not an underrun count.
func openOutput(src io.Reader) (output, error) {
	return webaudio.Open(src, webaudio.Options{SampleRate: engine.SampleRate, Queue: 80 * time.Millisecond})
}
