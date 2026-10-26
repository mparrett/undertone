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
// tests; nothing in it feeds back to the guest.
//
//	GOOS=js GOARCH=wasm go build -o undertone-bridge.wasm ./cmd/undertone-bridge
package main

import (
	"fmt"
	"sync/atomic"
	"syscall/js"

	"github.com/nooga/let-go/pkg/api"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/letgo"
)

const bridgeNS = `(ns undertone.bridge)
(defn handle! [[op a & more]]
  (case op
    :score      (undertone/set-parameters (apply ambience/derive-mood a more))
    :params     (undertone/set-parameters a)
    :trigger    (undertone/trigger a)
    :music-gain (undertone/set-music-gain a)
    nil))`

var received, errs atomic.Int64

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
	go m.Start()

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
			return map[string]any{"received": received.Load(), "errors": errs.Load(), "started": m.Started()}
		}),
	})
	fmt.Println("undertone-bridge: ready")
	select {}
}
