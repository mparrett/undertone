//go:build !js

package main

import (
	"io"
	"time"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/nativeaudio"
)

// startPlayback reads half a second ahead, as oto does by default: a CLI bed
// has no one-shots waiting behind the read-ahead, so it takes the slack.
func startPlayback(s io.Reader) (stop func(), err error) {
	p, err := nativeaudio.Open(s, nativeaudio.Options{SampleRate: engine.SampleRate, Queue: 500 * time.Millisecond})
	if err != nil {
		return nil, err
	}
	return p.Close, nil
}
