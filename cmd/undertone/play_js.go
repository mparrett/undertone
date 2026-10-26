//go:build js

package main

import (
	"io"
	"net/url"
	"strconv"
	"syscall/js"
	"time"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/webaudio"
)

// startPlayback opens the browser output and returns at once: webaudio.Open waits for
// the page's unlocking gesture, and the meter has to run meanwhile.
func startPlayback(s io.Reader) (stop func(), err error) {
	if syn, ok := s.(*engine.Synth); ok {
		exposeScenes(syn)
	}
	go func() {
		// A bed alone, with no one-shots waiting behind the queue, so it keeps
		// the slack (as the native CLI does) rather than webaudio's 80 ms.
		p, err := webaudio.Open(s, webaudio.Options{SampleRate: engine.SampleRate, Queue: 300 * time.Millisecond})
		if err != nil {
			println("undertone:", err.Error())
			return
		}
		keep = p
	}()
	return func() {}, nil
}

var keep *webaudio.Player

// exposeScenes lets the page switch scenes without reloading:
// window.undertoneScene("?mood=overworld&depth=3") takes the same query the
// page's flags come from, and the change is heard at once. A reload would
// cost a fresh tap to unlock audio on iOS.
func exposeScenes(syn *engine.Synth) {
	js.Global().Set("undertoneScene", js.FuncOf(func(_ js.Value, a []js.Value) any {
		q, err := url.ParseQuery(trimQuery(a[0].String()))
		if err != nil {
			return nil
		}
		depth, _ := strconv.Atoi(q.Get("depth"))
		mood, level := signedDepth(q.Get("mood"), depth)
		seed, err := strconv.ParseUint(q.Get("seed"), 10, 64)
		if err != nil {
			seed = 1
		}
		tension, _ := strconv.ParseFloat(q.Get("tension"), 64)
		syn.SetParametersNow(deriveParams(mood, seed, level, tension, q.Get("lead")))
		return nil
	}))
}

func trimQuery(s string) string {
	if len(s) > 0 && s[0] == '?' {
		return s[1:]
	}
	return s
}
