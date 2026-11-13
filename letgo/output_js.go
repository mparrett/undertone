//go:build js

package letgo

import (
	"io"
	"syscall/js"
	"time"

	"github.com/nooga/let-go/pkg/rt"
	"github.com/nooga/let-go/pkg/vm"

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
//
// In worker mode (one VM: the game's own worker renders, page.js plays) the
// pump shares its thread with the game loop rather than with the page, and
// 60 ms ran dry. Tesserae on let-go 18ce3194, TinyGo, headless Chromium under
// a load average of 4-5, 2026-10-31: 60 ms lost 103 quanta in 40 s and kept
// losing; 90 ms lost 241 in one 90 s run and none in another; 120 ms lost 3
// in 90 s, never dipping under 64 ms queued. On an iPhone 13 Pro 60, 120 and
// 160 all played clean by ear, so the margin is for a loaded worker, not the
// phone. Worker mode needs the ring, so it is never the message path.
// undertone/set-queue tunes any of these per device without a rebuild.
//
// A bundle built with lg -w -w-audio carries let-go's own browser device
// (nooga/let-go#1112, this same design moved into let-go), and then the music
// streams into its mix instead, beside the guest's audio/tone and
// audio/sample sounds, with no page.js. Its queue is the device's
// (LETGO_AUDIO_QUEUE, or audio.Default().Configure from Go), so
// undertone/set-queue does nothing there.
func openOutput(src io.Reader) (output, error) {
	if a, ok := seamDevice(); ok {
		return streamTo(a, src)
	}
	queue := 60 * time.Millisecond
	switch {
	case webaudio.InWorker():
		queue = 120 * time.Millisecond
	case !webaudio.SharedAvailable():
		queue = 80 * time.Millisecond
	}
	return webaudio.Open(src, webaudio.Options{SampleRate: engine.SampleRate, Queue: queue})
}

// seamDevice returns let-go's browser device when the bundle has one. Both
// halves come from -w-audio: pkg/audio, whose init binds *audio* (unbound
// otherwise, so nothing here links it), and lg-host-core.js, which defines
// _lgAudioOpen on the page and in its worker. undertone's own example pages
// have neither, so they keep webaudio.
func seamDevice() (rt.Audio, bool) {
	if js.Global().Get("_lgAudioOpen").Type() != js.TypeFunction {
		return nil, false
	}
	v := rt.LookupCoreVar("*audio*")
	if v == nil {
		return nil, false
	}
	b, ok := v.Root().(*vm.Boxed)
	if !ok {
		return nil, false
	}
	a, ok := b.Unbox().(rt.Audio)
	return a, ok
}
