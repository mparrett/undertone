//go:build !js

package main

import (
	"io"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/nativeaudio"
)

func startPlayback(s io.Reader) (stop func(), err error) {
	p, err := nativeaudio.Open(s, nativeaudio.Options{SampleRate: engine.SampleRate})
	if err != nil {
		return nil, err
	}
	return p.Close, nil
}
