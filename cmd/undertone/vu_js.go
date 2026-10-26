//go:build js

package main

import (
	"fmt"
	"syscall/js"
	"time"

	"github.com/mparrett/undertone/engine"
)

// runMeterUI publishes band levels to window.__homageMeter ~30x/s for the
// host page to draw. Never returns. No terminal meters here: wasm_exec's
// stdout shim buffers until newline, so a \r-redraw line would never flush.
func runMeterUI(s *engine.Synth, desc, _ string) {
	fmt.Printf("playing %s\n", desc)
	go func() {
		for range time.Tick(33 * time.Millisecond) {
			// Nothing until sound is out: the engine renders ahead while the
			// page waits for a tap, and a meter moving over silence reads as
			// "playing, but broken".
			if keep == nil {
				continue
			}
			v := s.Levels()
			js.Global().Set("__homageMeter", js.ValueOf([]any{v[0], v[1], v[2], v[3]}))
			// the output's health, for the page, probes and the console
			js.Global().Set("__undertoneAudio", js.ValueOf(map[string]any{
				"queuedMs": keep.Queued().Milliseconds(), "underruns": keep.Underruns(), "shared": keep.Shared(),
			}))
		}
	}()
	select {}
}
