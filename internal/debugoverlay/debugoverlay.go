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
// × closes the panel to a small debug tab in the same corner, which reopens
// it; the counts go on meanwhile, and the tab turns red as the panel would.
//
// copy puts the panel's numbers on the clipboard with the time, the page and
// the browser, as plain text to paste into a PR or an issue.
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
	// Top right, clear of a notch: the demos dock their own controls at the
	// bottom on a phone.
	const corner = "position:fixed;z-index:9999;" +
		"top:calc(8px + env(safe-area-inset-top));right:calc(8px + env(safe-area-inset-right));" +
		"font:12px/1.4 ui-monospace,Menlo,monospace;border-radius:6px;" +
		"background:rgba(0,0,0,.78);color:#e8e8e8;pointer-events:auto;"
	box.Get("style").Set("cssText", corner+"white-space:pre;padding:8px 40px 8px 10px")
	closeBtn := doc.Call("createElement", "button")
	closeBtn.Set("textContent", "×")
	closeBtn.Set("title", "close; the debug tab reopens it")
	// 32 px square: a near miss on a phone would land on the text, which folds
	closeBtn.Get("style").Set("cssText", "position:absolute;top:0;right:0;width:32px;height:32px;padding:0;"+
		"font:18px/32px sans-serif;color:inherit;background:none;border:0;cursor:pointer")
	tab := doc.Call("createElement", "button")
	tab.Set("id", "undertone-debug-tab")
	tab.Set("textContent", "debug")
	tab.Set("title", "reopen the debug panel")
	tab.Get("style").Set("cssText", corner+"display:none;padding:4px 8px;border:0;cursor:pointer")
	open := func(o bool) {
		show(box, o)
		show(tab, !o)
	}
	closeBtn.Call("addEventListener", "click", js.FuncOf(func(js.Value, []js.Value) any {
		open(false)
		return nil
	}))
	tab.Call("addEventListener", "click", js.FuncOf(func(js.Value, []js.Value) any {
		open(true)
		return nil
	}))
	text := doc.Call("createElement", "div")
	text.Set("title", "tap to fold or unfold")
	text.Get("style").Set("cursor", "pointer")
	button := func(label string) js.Value {
		b := doc.Call("createElement", "button")
		b.Set("textContent", label)
		b.Get("style").Set("cssText", "margin:6px 6px 0 0;font:inherit;cursor:pointer")
		return b
	}
	reset, copyBtn := button("reset"), button("copy")
	buttons := doc.Call("createElement", "div")
	buttons.Call("append", reset, copyBtn)
	// where the clipboard is unavailable, the report is shown here to copy by hand
	manual := doc.Call("createElement", "textarea")
	manual.Set("readOnly", true)
	manual.Get("style").Set("cssText", "display:none;margin-top:6px;width:30em;max-width:80vw;height:10em;font:inherit")
	box.Call("append", closeBtn, text, buttons, manual)
	doc.Get("body").Call("append", box, tab)

	var (
		base    counts    // counters at the last reset
		opened  time.Time // when player first returned an output
		lowest  = time.Duration(-1)
		pending atomic.Bool // a reset waits for the next tick, on the overlay's goroutine
		folded  atomic.Bool // one line, for a small screen
		resetAt time.Time
		report  atomic.Pointer[string] // the latest, for copy
	)
	// a phone that never starts audio is the report that most needs the header
	none := header() + "audio not started\n"
	report.Store(&none)
	// Made once, not per tap: a js.Func lives until released.
	var (
		copied, showManual js.Func
		relabel            *time.Timer // puts "copy" back after "copied"
	)
	stopRelabel := func() {
		if relabel != nil {
			relabel.Stop()
		}
	}
	copied = js.FuncOf(func(js.Value, []js.Value) any {
		stopRelabel()
		manual.Get("style").Set("display", "none")
		copyBtn.Set("textContent", "copied")
		relabel = time.AfterFunc(1500*time.Millisecond, func() { copyBtn.Set("textContent", "copy") })
		return nil
	})
	showManual = js.FuncOf(func(js.Value, []js.Value) any {
		stopRelabel()
		manual.Set("value", *report.Load())
		manual.Get("style").Set("display", "block")
		manual.Call("select")
		copyBtn.Set("textContent", "select and copy")
		return nil
	})
	copyBtn.Call("addEventListener", "click", js.FuncOf(func(js.Value, []js.Value) any {
		// Inside the tap's handler: browsers grant the clipboard only to a
		// user gesture, and only in a secure context.
		clip := js.Global().Get("navigator").Get("clipboard")
		if clip.IsUndefined() {
			showManual.Invoke()
			return nil
		}
		clip.Call("writeText", *report.Load()).Call("then", copied, showManual)
		return nil
	}))
	pending.Store(true)
	reset.Call("addEventListener", "click", js.FuncOf(func(js.Value, []js.Value) any {
		pending.Store(true)
		return nil
	}))
	fold := func(f bool) {
		folded.Store(f)
		show(buttons, !f)
		if f {
			show(manual, false)
		}
	}
	text.Call("addEventListener", "click", js.FuncOf(func(js.Value, []js.Value) any {
		fold(!folded.Load())
		return nil
	}))
	// On a phone the open panel covers the top of the page; one line does not.
	fold(js.Global().Get("innerWidth").Int() < 600)

	go func() {
		for range time.Tick(tick) {
			p := player()
			if p == nil {
				text.Set("textContent", "undertone debug\ntap the page to start audio")
				r := header() + "audio not started\n"
				report.Store(&r)
				continue
			}
			if opened.IsZero() {
				opened = time.Now()
			}
			now := read(syn, p)
			if pending.Swap(false) {
				base, lowest, resetAt = now, -1, time.Now()
			}
			if time.Since(opened) >= warmup && (lowest < 0 || now.queued < lowest) {
				lowest = now.queued
			}
			d := now.since(base)
			r := d.report(now, lowest, time.Since(resetAt))
			report.Store(&r)
			if folded.Load() {
				text.Set("textContent", fmt.Sprintf("debug: %d underruns, %d over", d.underruns, d.over))
			} else {
				text.Set("textContent", d.format(now, lowest))
			}
			color := "#e8e8e8"
			if d.underruns > 0 || d.over > 0 {
				color = "#ff8a80"
			}
			text.Get("style").Set("color", color)
			tab.Get("style").Set("color", color) // a closed panel still shows trouble
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

// report is the panel's numbers as text to paste: the header, then what the
// panel shows.
func (c counts) report(now counts, lowest, sinceReset time.Duration) string {
	lines := strings.SplitN(c.format(now, lowest), "\n", 2)
	return fmt.Sprintf("%ssince reset (%.0f s):\n%s\n", header(), sinceReset.Seconds(), lines[len(lines)-1])
}

// header is when, where and in what: the browser line is what a failed step
// most needs and a tester most often leaves out. The page is its path alone,
// since the report is meant for a public PR and a host can name a machine
// (tailscale serve's do).
func header() string {
	g := js.Global()
	loc := g.Get("location")
	return fmt.Sprintf("undertone debug, %s\npage      %s%s\nbrowser   %s\n",
		time.Now().Format("2006-01-02 15:04:05"),
		loc.Get("pathname").String(), loc.Get("search").String(),
		g.Get("navigator").Get("userAgent").String())
}

func show(el js.Value, on bool) {
	display := "none"
	if on {
		display = ""
	}
	el.Get("style").Set("display", display)
}

func ms(d time.Duration) float64 {
	if d < 0 {
		return -1
	}
	return float64(d) / float64(time.Millisecond)
}
