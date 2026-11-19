//go:build js

// Package debugoverlay draws a browser demo's output health in a corner of
// the page when its URL has ?debug: underruns, renders that overran their
// budget, the slowest render, and how full the queue is. Listening catches a
// long dropout; a short one on a phone is easy to miss, and these counters
// do not miss it. The examples' manual browser checklist reads them
// (examples/README.md).
//
// Underruns are the count to trust. The lowest queue is sampled every tick on
// the page's main thread, so it misses a dip the main thread caused itself: a
// stall that empties the queue also stops the sampling until the pump has
// refilled it.
//
// The same numbers are published as window.__undertoneDebug, for a console or
// an automated probe.
package debugoverlay

import (
	"fmt"
	"strings"
	"sync/atomic"
	"syscall/js"
	"time"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/webaudio"
)

const (
	tick = 500 * time.Millisecond
	// warmup is how long after the output opens before the lowest queue is
	// tracked: the queue starts empty and fills, which is not a dip.
	warmup = 2 * time.Second
)

// Enabled reports whether the page's URL asks for the overlay.
func Enabled() bool {
	q := js.Global().Get("URLSearchParams").New(js.Global().Get("location").Get("search"))
	return q.Call("has", "debug").Bool()
}

// Show starts the overlay if the page asked for it, and returns at once.
// player returns the output once it is open, nil before then: outputs open
// on the page's first tap.
func Show(syn *engine.Synth, player func() *webaudio.Player) {
	if !Enabled() {
		return
	}
	doc := js.Global().Get("document")
	box := doc.Call("createElement", "div")
	box.Set("id", "undertone-debug")
	box.Get("style").Set("cssText", "position:fixed;right:8px;bottom:8px;z-index:9999;"+
		"font:12px/1.4 ui-monospace,Menlo,monospace;white-space:pre;padding:8px 10px;"+
		"border-radius:6px;background:rgba(0,0,0,.78);color:#e8e8e8;pointer-events:auto")
	text := doc.Call("createElement", "div")
	reset := doc.Call("createElement", "button")
	reset.Set("textContent", "reset")
	reset.Get("style").Set("cssText", "margin-top:6px;font:inherit;cursor:pointer")
	box.Call("append", text, reset)
	doc.Get("body").Call("append", box)

	var (
		base    counts    // counters at the last reset
		opened  time.Time // when player first returned an output
		lowest  = time.Duration(-1)
		pending atomic.Bool // a reset waits for the next tick, on the overlay's goroutine
	)
	pending.Store(true)
	reset.Call("addEventListener", "click", js.FuncOf(func(js.Value, []js.Value) any {
		pending.Store(true)
		return nil
	}))

	go func() {
		for range time.Tick(tick) {
			p := player()
			if p == nil {
				text.Set("textContent", "undertone debug\ntap the page to start audio")
				continue
			}
			if opened.IsZero() {
				opened = time.Now()
			}
			now := read(syn, p)
			if pending.Swap(false) {
				base, lowest = now, -1
			}
			if time.Since(opened) >= warmup && (lowest < 0 || now.queued < lowest) {
				lowest = now.queued
			}
			d := now.since(base)
			text.Set("textContent", d.format(now, lowest))
			color := "#e8e8e8"
			if d.underruns > 0 || d.over > 0 {
				color = "#ff8a80"
			}
			text.Get("style").Set("color", color)
			js.Global().Set("__undertoneDebug", js.ValueOf(map[string]any{
				"underruns": d.underruns, "over": d.over, "reads": d.reads,
				"slowestMs": ms(now.slowest), "queuedMs": ms(now.queued), "lowestQueuedMs": ms(lowest),
				"shared": now.shared,
			}))
		}
	}()
}

// counts is one reading of the output's counters.
type counts struct {
	underruns, over, reads int64
	slowest, queued        time.Duration
	shared                 bool
}

func read(syn *engine.Synth, p *webaudio.Player) counts {
	r := syn.RenderStats()
	return counts{
		underruns: p.Underruns(), over: r.Over, reads: r.Reads,
		slowest: r.Max, queued: p.Queued(), shared: p.Shared(),
	}
}

// since is c's counters less base's. The slowest render is since load: the
// engine keeps only its maximum, which a reset cannot take back.
func (c counts) since(base counts) counts {
	c.underruns -= base.underruns
	c.over -= base.over
	c.reads -= base.reads
	return c
}

func (c counts) format(now counts, lowest time.Duration) string {
	ring := "messages"
	if now.shared {
		ring = "shared memory"
	}
	low := "warming up"
	if lowest >= 0 {
		low = fmt.Sprintf("%.0f ms", ms(lowest))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "undertone debug (since reset)\n")
	fmt.Fprintf(&b, "underruns     %d\n", c.underruns)
	fmt.Fprintf(&b, "over budget   %d of %d renders\n", c.over, c.reads)
	fmt.Fprintf(&b, "slowest       %.1f ms (since load)\n", ms(now.slowest))
	fmt.Fprintf(&b, "queued        %.0f ms, lowest %s\n", ms(now.queued), low)
	fmt.Fprintf(&b, "ring          %s", ring)
	return b.String()
}

func ms(d time.Duration) float64 {
	if d < 0 {
		return -1
	}
	return float64(d) / float64(time.Millisecond)
}
