package main

import (
	"errors"

	"github.com/mparrett/undertone/engine"
)

func playMIDILive(*engine.Synth, engine.Params, string) error {
	return errors.New("-midi-live is not available in the browser")
}
