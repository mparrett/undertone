//go:build js

// undertone-bridge plays undertone on the page for a let-go guest that runs
// in a Web Worker, as `lg -w` bundles do. A worker has no AudioContext, so
// the guest can't open webaudio itself. Instead it sends control messages
// with js/emit, and this module, loaded on the main thread beside the guest,
// runs them through its own small lg VM with the `audio` namespace and the
// embedded `ambience`.
//
// Wire format: a window CustomEvent named "undertone" whose detail is
// {"form": "<edn>"}, the EDN being one command vector:
//
//	[:score mood seed level tension]   ambience/derive-mood, then set-parameters
//	[:params {...}]                    undertone/set-parameters
//	[:trigger m-or-vector]             undertone/trigger
//	[:music-gain g]                    undertone/set-music-gain
//
// Only control-rate data crosses: a parameter map every few seconds and the
// odd one-shot, never PCM. The device opens at load and starts on the first
// gesture anywhere on the page (webaudio listens for it), so commands sent
// before then update state and triggers drop, as undertone/trigger documents.
//
// window.undertoneBridge.stats() reports {received, errors, started} for
// tests, and once playing {queuedMs, underruns, queueMs} for latency work;
// nothing in it feeds back to the guest. setQueue(ms) changes how much audio
// webaudio keeps queued ahead of the speaker (letgo's 80 ms until set), and
// returns the value in effect, or null before audio starts; ?queue=<ms> on
// the page's URL applies one when it does.
//
//	GOOS=js GOARCH=wasm go build -o undertone-bridge.wasm ./cmd/undertone-bridge
package main

import (
	"fmt"
	"strconv"
	"sync/atomic"
	"syscall/js"
	"time"

	"github.com/nooga/let-go/pkg/api"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/letgo"
	"github.com/mparrett/undertone/webaudio"
)

const bridgeNS = `(ns undertone.bridge)
(defn handle! [[op a & more]]
  (case op
    :score      (undertone/set-parameters (apply ambience/derive-mood a more))
    :params     (undertone/set-parameters a)
    :trigger    (undertone/trigger a)
    :music-gain (undertone/set-music-gain a)
    nil))`

var received, errs, queueMs atomic.Int64 // queueMs: the last setQueue in effect, 0 for letgo's default

func main() {
	m := letgo.NewMusic(engine.DeriveDungeon(1, 0, 0))
	letgo.SetMusic(m)

	c, err := api.NewLetGo("undertone.bridge")
	if err == nil {
		err = letgo.LoadEmbedded(c)
	}
	if err == nil {
		err = letgo.LoadSource(c, "undertone.bridge", bridgeNS)
	}
	if err != nil {
		js.Global().Get("console").Call("error", "undertone-bridge: "+err.Error())
		return
	}

	// Open now and let Start block until the unlocking gesture: creating the
	// context inside a later handler misses that gesture (see descent/web).
	go func() {
		m.Start()
		q := js.Global().Get("URLSearchParams").New(js.Global().Get("location").Get("search")).Call("get", "queue")
		if ms, err := strconv.Atoi(q.String()); q.Truthy() && err == nil {
			setQueue(m, ms)
		}
	}()

	// Handlers must not block the wasm scheduler, so they only enqueue.
	msgs := make(chan string, 256)
	go func() {
		for form := range msgs {
			if err := c.Def("undertone-bridge-msg", form); err != nil {
				errs.Add(1)
				continue
			}
			if _, err := c.Run("(undertone.bridge/handle! (read-string undertone-bridge-msg))"); err != nil {
				errs.Add(1)
				js.Global().Get("console").Call("warn", "undertone-bridge: "+err.Error())
			}
		}
	}()

	js.Global().Call("addEventListener", "undertone", js.FuncOf(func(_ js.Value, a []js.Value) any {
		form := a[0].Get("detail").Get("form")
		if form.Type() != js.TypeString {
			errs.Add(1)
			return nil
		}
		received.Add(1)
		select {
		case msgs <- form.String():
		default:
			errs.Add(1) // a burst beyond the queue drops, like a full voice pool
		}
		return nil
	}))
	js.Global().Set("undertoneBridge", map[string]any{
		"stats": js.FuncOf(func(js.Value, []js.Value) any {
			s := map[string]any{"received": received.Load(), "errors": errs.Load(), "started": m.Started()}
			if t := m.Tuner(); t != nil {
				s["queuedMs"], s["underruns"], s["queueMs"] = t.Queued().Milliseconds(), t.Underruns(), queueMs.Load()
			}
			return s
		}),
		"setQueue": js.FuncOf(func(_ js.Value, a []js.Value) any {
			if len(a) != 1 || a[0].Type() != js.TypeNumber {
				return nil
			}
			return setQueue(m, a[0].Int())
		}),
	})
	fmt.Println("undertone-bridge: ready")
	select {}
}

// The browser output must stay a Tuner, or m.Tuner() is silently nil.
var _ letgo.Tuner = (*webaudio.Player)(nil)

// setQueue sets webaudio's queue to ms and returns the value in effect, or nil
// before audio has started. Depends on webaudio.Player.SetQueue (#74).
func setQueue(m *letgo.Music, ms int) any {
	t := m.Tuner()
	if t == nil {
		return nil
	}
	got := t.SetQueue(time.Duration(ms) * time.Millisecond).Milliseconds()
	queueMs.Store(got)
	return got
}
