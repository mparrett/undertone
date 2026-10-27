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
// page's main thread beside the terminal, 2026-10-26), its audiolab panel on
// an iPhone 13 Pro counted no underruns over minutes of play at 60, 80, 120
// and 200 ms, and 8 at 40 ms. On desktop Chrome a trigger reached the
// worklet's output 69 ms after dispatch at 60, against 100 ms at 80. The
// price is stall tolerance: a main-thread stall longer than the queue (a GC,
// a heavy redraw) empties the ring and is heard as a gap, where 300 ms rode
// out 120 ms stalls.
//
// tesserae is served cross-origin isolated, so all of that is the
// SharedArrayBuffer ring. The message path webaudio falls back to elsewhere
// counts posted slices as queued before the worklet has them, and on a phone
// dipped ~30 ms deeper than the ring at 300 ms; unmeasured at 60, it keeps
// the 80 ms it had.
func openOutput(src io.Reader) (output, error) {
	queue := 60 * time.Millisecond
	if !webaudio.Shared() {
		queue = 80 * time.Millisecond
	}
	return webaudio.Open(src, webaudio.Options{SampleRate: engine.SampleRate, Queue: queue})
}
