package main

import (
	"errors"

	"github.com/mparrett/undertone/engine"
)

// setLayers has nothing to limit in the browser, where -midi-live fails
// anyway.
func setLayers(string) error { return nil }

func playMIDILive(*engine.Synth, engine.Params, string, bool) error {
	return errors.New("-midi-live is not available in the browser")
}
