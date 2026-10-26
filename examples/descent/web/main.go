//go:build js

// The descent demo's browser front end: the identical audio surface
// (undertone/letgo, driven by ../host), the identical lg lane — loaded from embedded
// sources via load-string, since GOOS=js has no filesystem — and the same
// scripted arc, with the trace going to the page instead of stdout.
//
// It is also the browser proof of the one-shot lane: music parameters and
// sfx triggers both crossing the lg->Go boundary inside wasm, out through
// the webaudio package's worklet.
//
//	GOOS=js GOARCH=wasm go build -o examples/descent/web/main.wasm ./examples/descent/web
//	cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/descent/web/
//	python3 examples/serve.py 8901 descent/web
package main

import (
	"fmt"
	"sync/atomic"
	"syscall/js"
	"time"

	"github.com/nooga/let-go/pkg/api"
	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/letgo"

	"github.com/mparrett/undertone/examples/descent/host"
)

var doc = js.Global().Get("document")

func setText(id, s string) { doc.Call("getElementById", id).Set("textContent", s) }

func appendLine(s string) {
	pre := doc.Call("getElementById", "trace")
	pre.Set("textContent", pre.Get("textContent").String()+s+"\n")
	pre.Set("scrollTop", pre.Get("scrollHeight"))
}

func main() {
	m := letgo.NewMusic(engine.DeriveDungeon(1, 0, 0))
	letgo.SetMusic(m)

	c, err := api.NewLetGo("undertone.web")
	if err != nil {
		setText("status", "let-go failed to boot: "+err.Error())
		return
	}
	if err := host.LoadEmbedded(c); err != nil {
		setText("status", "lg load failed: "+err.Error())
		return
	}

	// Open the audio device NOW, at load, and let Start block in the
	// background: oto's ready channel only closes on a touchend/keyup/mouseup
	// that arrives AFTER the AudioContext exists. Creating the context inside
	// the play handler is the trap — the tap's own touchend has already fired
	// by then, so ready never closes and Start hangs forever. Opened at load,
	// the play tap itself is the unlocking gesture.
	go func() {
		m.Start()
		fmt.Println("descent: audio device open:", m.Started())
	}()
	setText("status", "ready: tap to play the arc")

	var arc arcState
	btn := doc.Call("getElementById", "play")
	stop := doc.Call("getElementById", "stop")
	btn.Set("disabled", false)
	// Both handlers stay synchronous and only flip state: blocking a JS
	// callback deadlocks the wasm scheduler. Resume runs here, inside the
	// tap, because iOS only restarts audio from within a gesture.
	btn.Call("addEventListener", "click", js.FuncOf(func(js.Value, []js.Value) any {
		switch arc.get() {
		case idle, done:
			arc.set(playing)
			m.Resume()
			go runArc(c, m, &arc, arc.run.Add(1))
		case playing:
			arc.set(paused)
			m.Pause()
		case paused:
			arc.set(playing)
			m.Resume()
		}
		showButtons(arc.get())
		return nil
	}))
	stop.Call("addEventListener", "click", js.FuncOf(func(js.Value, []js.Value) any {
		arc.set(idle)
		arc.run.Add(1)
		m.Pause()
		showButtons(idle)
		setText("status", "stopped: tap to play the arc from the top")
		return nil
	}))

	select {} // keep the wasm module alive for the event handlers
}

type phase int32

const (
	idle phase = iota
	playing
	paused
	done
)

// arcState is the page's play state. The arc goroutine polls it between
// turns, so pause and stop take effect within a tenth of a second. run
// numbers each start; a goroutine whose run is no longer current exits, so a
// quick stop-then-play can't leave two arcs going.
type arcState struct{ v, run atomic.Int32 }

func (a *arcState) get() phase  { return phase(a.v.Load()) }
func (a *arcState) set(p phase) { a.v.Store(int32(p)) }

func showButtons(p phase) {
	btn := doc.Call("getElementById", "play")
	label := map[phase]string{idle: "▶ play the arc", playing: "❚❚ pause", paused: "▶ resume", done: "↻ play again"}[p]
	btn.Set("textContent", label)
	doc.Call("getElementById", "stop").Set("hidden", p == idle)
}

// wait sleeps d of playing time: paused time doesn't count, and it returns
// false as soon as this run is stopped or superseded.
func wait(a *arcState, run int32, d time.Duration) bool {
	for d > 0 {
		if a.run.Load() != run {
			return false
		}
		switch a.get() {
		case idle:
			return false
		case paused:
			time.Sleep(100 * time.Millisecond)
			continue
		}
		step := min(d, 100*time.Millisecond)
		time.Sleep(step)
		d -= step
	}
	return a.run.Load() == run && a.get() != idle
}

// turnTime is the real time per game turn. It is close to a pattern's
// length, so the music has time to answer one turn before the next arrives.
const turnTime = 5400 * time.Millisecond

func runArc(c *api.LetGo, m *letgo.Music, a *arcState, run int32) {
	setText("status", "unlocking audio…")
	for i := 0; !m.Started(); i++ {
		if i == 40 { // ~4s: something else is wrong (no output device?)
			setText("status", "audio never unlocked: tap once more, or see console")
		}
		if !wait(a, run, 100*time.Millisecond) {
			return
		}
	}
	setText("trace", "")
	n, err := host.DemoTurns(c)
	if err != nil {
		setText("status", err.Error())
		return
	}
	tension := 0.0
	for i := 0; i < n; i++ {
		var line string
		tension, line, err = host.DemoStep(c, m, i, tension)
		if err != nil {
			setText("status", fmt.Sprintf("turn %d: %v", i+1, err))
			return
		}
		appendLine(line)
		hit, _ := c.Run(fmt.Sprintf("(demo/hit? %d)", i))
		if hit == vm.TRUE {
			setText("status", "hit!")
		} else {
			setText("status", "playing: parameter changes land at pattern boundaries (~8s)")
		}
		next, _ := c.Run(fmt.Sprintf("(demo/hit? %d)", i+1))
		if i+1 < n && next == vm.TRUE {
			// Count the next blow in over the last three seconds of this
			// turn, so the ear knows when to listen for it.
			if !wait(a, run, turnTime-3*time.Second) {
				return
			}
			for k := 3; k >= 1; k-- {
				setText("status", fmt.Sprintf("hit incoming… %d", k))
				if !wait(a, run, time.Second) {
					return
				}
			}
		} else if !wait(a, run, turnTime) {
			return
		}
	}
	// No Stop here: the last scene keeps humming, which is the nicer ending;
	// the stop button silences it.
	if a.run.Load() != run {
		return
	}
	a.set(done)
	showButtons(done)
	setText("status", "done: the deep keeps playing")
}
