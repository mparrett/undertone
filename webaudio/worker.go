//go:build js

package webaudio

import (
	"errors"
	"io"
	"math"
	"sync/atomic"
	"syscall/js"
)

// inWorker reports whether this is a Web Worker: no document, so no
// AudioContext, but the page that started it has one.
func inWorker() bool {
	g := js.Global()
	return g.Get("document").IsUndefined() && !g.Get("WorkerGlobalScope").IsUndefined()
}

// openWorker is Open inside a Web Worker, for a program that runs off the main
// thread (a let-go guest that blocks on keys, say) and still wants sound. The
// worker renders into the shared ring as usual; page.js on the main thread
// creates the AudioContext and the worklet that reads it.
//
// The ring goes to the page through the worker's own postMessage, as
// "undertone:open". It can't go over a BroadcastChannel: Chrome delivers a
// SharedArrayBuffer sent that way as null (checked 2026-10-15), while
// postMessage between a worker and its page shares it. The worker's creator may
// be someone else's runtime with its own message protocol, so page.js listens
// on the Worker itself and the messages carry a prefixed type that such a
// handler ignores.
//
// Unlike the main-thread Open it returns as soon as the ring is primed and
// announced: there is no gesture to wait for here, the page handles that, and
// the source's clock only advances as the pump renders, so nothing is lost
// before the page starts playing.
func openWorker(src io.Reader, opt Options) (*Player, error) {
	g := js.Global()
	if !SharedAvailable() {
		return nil, errors.New("webaudio: playing from a worker needs a cross-origin isolated page (SharedArrayBuffer)")
	}
	p := &Player{src: src, opt: opt, shared: true, worker: true, id: int(playerIDs.Add(1))}
	p.cap = opt.Slice * int(math.Ceil(float64(opt.SampleRate)/float64(opt.Slice)))
	sab := g.Get("SharedArrayBuffer")
	samples, ctrl := sab.New(p.cap*8), sab.New(12)
	p.data = g.Get("Uint8Array").New(samples)
	p.ctrl = g.Get("Int32Array").New(ctrl)
	p.cacheViews()
	p.SetQueue(opt.Queue)

	for p.queued() < p.target() {
		if err := p.push(); err != nil {
			return nil, err
		}
	}
	g.Call("postMessage", map[string]any{
		"t": "undertone:open", "id": p.id, "sampleRate": opt.SampleRate, "cap": p.cap,
		"data": samples, "ctrl": ctrl, "processor": processor,
	})
	go p.pump()
	return p, nil
}

// playerIDs numbers this worker's Players. page.js keys players by worker as
// well as by id, so ids need only be unique within one worker.
var playerIDs atomic.Int64

// tell sends the page a control message for a worker-mode Player.
func (p *Player) tell(t string) {
	js.Global().Call("postMessage", map[string]any{"t": "undertone:" + t, "id": p.id})
}
