//go:build js

// Package webaudio plays a PCM stream in the browser without oto.
//
// oto's js backend synthesizes on demand: its mixer goroutine reads a whole
// player buffer from the source when the worklet asks for a refill. Go wasm
// has one thread and no preemption, so that burst runs inside the refill
// deadline, and on a phone it overruns it — a regular crackle on iOS Safari
// that no buffer sizing removed (2048/500 ms crackled slowly, 512/40 ms
// quickly, 2048/40 ms zero-padded every request).
//
// This package decouples the two. An AudioWorklet only copies frames out of a
// ring it owns; a goroutine renders small slices between other work, within a
// per-tick budget, until the ring holds Queue's worth, then sleeps. The queue
// absorbs a slow slice or a busy main thread instead of the audio thread
// paying for it. Measured on an iPhone over HTTPS: clean, 0 underruns, lowest
// queue 267 ms of 300, including with 120 ms main-thread stalls each second.
//
// The queue is also latency: a trigger is rendered behind everything already
// queued, so at 300 ms a one-shot landed 100-300 ms after what caused it. The
// default is 80 ms since 2026-10-26: playtesting tesserae on an iPhone 13 Pro
// (TinyGo 0.43 bridge, shared ring, the game's worker drawing alongside), 80
// sounded tighter than 140 and 300, with no clicks heard. That is ear
// evidence, not an underrun count. SetQueue changes it while playing.
//
// Where the page is cross-origin isolated, the ring is a SharedArrayBuffer
// the pump writes straight into; elsewhere (most hosting, itch.io embeds
// without isolation) slices reach the worklet as transferred messages. Both
// were clean on the phone; the shared ring held its queue steadier (lowest
// 296 ms vs 267, ticks at most 13 ms apart).
//
// Open also works inside a Web Worker, which has no AudioContext: the pump
// stays in the worker and page.js plays the ring on the main thread (see
// worker.go).
package webaudio

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"syscall/js"
	"time"
)

// Options sizes the pump. Zero values take the defaults in brackets.
type Options struct {
	SampleRate int           // [44100] of the source; the context is opened at this rate
	Slice      int           // [512] frames rendered per step
	Queue      time.Duration // [80 ms] audio kept queued ahead of the speaker; see SetQueue
	Budget     time.Duration // [3 ms] longest render run before yielding to the page
}

// Player streams an io.Reader of interleaved stereo signed 16-bit
// little-endian frames to the page's audio output.
type Player struct {
	src  io.Reader
	opt  Options
	ctx  js.Value
	node js.Value

	sent, played atomic.Int64 // frames posted to / consumed by the worklet
	underruns    atomic.Int64
	queue        atomic.Int64 // frames to keep queued (Options.Queue, then SetQueue)
	closed       atomic.Bool
	held         atomic.Bool // paused on purpose: gestures must not resume it
	closeOnce    sync.Once
	funcs        []js.Func
	raw, f32     []byte // one slice of source frames, and as float32

	// shared ring, when cross-origin isolated: data is a Uint8Array over the
	// samples, ctrl an Int32Array of [write, read, underruns] in frames
	shared     bool
	data, ctrl js.Value
	cap        int

	// Made once, not per slice: under TinyGo every js.Value that holds a JS
	// object carries a finalizer. Three per slice (~86 slices a second) plus
	// one per queued() check came to several hundred a second, which forced
	// a full collection of the guest VM's heap often enough to stall the page
	// (TinyGo 0.42, 2026-10-15).
	atomics js.Value
	views   []js.Value // data.subarray for each slice-sized slot of the ring

	// worker mode: no ctx or node here; page.js plays the ring, and id
	// names this Player in its messages, since a worker may hold several
	worker bool
	id     int
}

// The worklet never allocates while playing: its ring is sized once, and an
// incoming slice that would overflow it is dropped and counted (the pump
// counts in-flight slices, so this is a guard, not a path).
const processor = `class UndertoneRing extends AudioWorkletProcessor {
  constructor(o) { super(); this.cap = o.processorOptions.cap; this.buf = new Float32Array(this.cap * 2);
    this.w = 0; this.r = 0; this.fill = 0; this.played = 0; this.under = 0; this.over = 0; this.n = 0;
    this.port.onmessage = (e) => { const d = e.data, f = d.length / 2;
      if (this.fill + f > this.cap) { this.over++; return; }
      for (let i = 0; i < f; i++) { this.buf[this.w * 2] = d[i * 2]; this.buf[this.w * 2 + 1] = d[i * 2 + 1]; this.w = (this.w + 1) % this.cap; }
      this.fill += f; }; }
  process(_, outs) { const L = outs[0][0], R = outs[0][1]; let short = false;
    for (let i = 0; i < L.length; i++) {
      if (this.fill > 0) { L[i] = this.buf[this.r * 2]; R[i] = this.buf[this.r * 2 + 1]; this.r = (this.r + 1) % this.cap; this.fill--; this.played++; }
      else { L[i] = 0; R[i] = 0; short = true; } }
    if (short && this.played > 0) this.under++;
    if (++this.n % 4 === 0) this.port.postMessage([this.played, this.under, this.over]);
    return true; } }
registerProcessor('undertone-ring', UndertoneRing);
class UndertoneShared extends AudioWorkletProcessor {
  constructor(o) { super(); this.cap = o.processorOptions.cap; this.buf = new Float32Array(o.processorOptions.data);
    this.c = new Int32Array(o.processorOptions.ctrl); this.started = false; }
  process(_, outs) { const L = outs[0][0], R = outs[0][1], c = this.c, cap = this.cap;
    const w = Atomics.load(c, 0); let r = c[1], avail = (w - r + cap) % cap, short = false;
    for (let i = 0; i < L.length; i++) {
      if (avail > 0) { L[i] = this.buf[r * 2]; R[i] = this.buf[r * 2 + 1]; r = (r + 1) % cap; avail--; this.started = true; }
      else { L[i] = 0; R[i] = 0; short = true; } }
    Atomics.store(c, 1, r);
    if (short && this.started) Atomics.add(c, 2, 1);
    return true; } }
registerProcessor('undertone-shared', UndertoneShared);`

// Open creates the AudioContext and starts the pump. It must be called before
// the user's unlocking gesture, not inside it: iOS Safari only lets a context
// start from a gesture that arrives after the context exists, so Open listens
// for the next touch, click or key and resumes from there. It returns once
// audio is running, which may be never if no gesture comes; call it from a
// goroutine, never from a js.FuncOf callback (blocking one deadlocks wasm).
//
// In a Web Worker it returns once the ring is primed instead, and needs
// page.js on the page and cross-origin isolation (see openWorker).
func Open(src io.Reader, opt Options) (*Player, error) {
	if opt.SampleRate == 0 {
		opt.SampleRate = 44100
	}
	if opt.Slice == 0 {
		opt.Slice = 512
	}
	if opt.Queue == 0 {
		opt.Queue = 80 * time.Millisecond
	}
	if opt.Budget == 0 {
		opt.Budget = 3 * time.Millisecond
	}
	if inWorker() {
		return openWorker(src, opt)
	}
	ac := js.Global().Get("AudioContext")
	if ac.IsUndefined() {
		ac = js.Global().Get("webkitAudioContext")
	}
	if ac.IsUndefined() {
		return nil, errors.New("webaudio: no AudioContext in this browser")
	}
	ctx := ac.New(map[string]any{"sampleRate": opt.SampleRate})
	if ctx.Get("audioWorklet").IsUndefined() {
		// Safari exposes AudioWorklet only in a secure context.
		ctx.Call("close")
		return nil, errors.New("webaudio: no AudioWorklet; the page must be served over HTTPS (or localhost)")
	}
	p := &Player{src: src, opt: opt, ctx: ctx}
	p.unlockOnGesture()

	url := js.Global().Get("URL").Call("createObjectURL",
		js.Global().Get("Blob").New([]any{processor}, map[string]any{"type": "application/javascript"}))
	if err := await(ctx.Get("audioWorklet").Call("addModule", url)); err != nil {
		p.Close()
		return nil, err
	}
	// ~1 s in whole slices, so a slice never wraps the shared ring
	p.cap = opt.Slice * int(math.Ceil(float64(opt.SampleRate)/float64(opt.Slice)))
	if js.Global().Get("crossOriginIsolated").Truthy() {
		p.shared = true
		sab := js.Global().Get("SharedArrayBuffer")
		samples, ctrl := sab.New(p.cap*8), sab.New(12)
		p.data = js.Global().Get("Uint8Array").New(samples)
		p.ctrl = js.Global().Get("Int32Array").New(ctrl)
		p.cacheViews()
		p.node = js.Global().Get("AudioWorkletNode").New(ctx, "undertone-shared", map[string]any{
			"outputChannelCount": []any{2},
			"processorOptions":   map[string]any{"cap": p.cap, "data": samples, "ctrl": ctrl},
		})
	} else {
		p.node = js.Global().Get("AudioWorkletNode").New(ctx, "undertone-ring", map[string]any{
			"outputChannelCount": []any{2},
			"processorOptions":   map[string]any{"cap": p.cap},
		})
		p.listen()
	}

	// Prime the queue before connecting, so the first quantum has audio.
	for p.queued() < p.target() {
		if err := p.push(); err != nil {
			p.Close()
			return nil, err
		}
	}
	p.node.Call("connect", ctx.Get("destination"))
	go p.pump()

	for ctx.Get("state").String() != "running" {
		if p.closed.Load() {
			return nil, errors.New("webaudio: closed before audio started")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return p, nil
}

// listen takes the message ring's position reports.
func (p *Player) listen() {
	onStats := js.FuncOf(func(_ js.Value, a []js.Value) any {
		d := a[0].Get("data")
		p.played.Store(int64(d.Index(0).Int()))
		p.underruns.Store(int64(d.Index(1).Int()))
		return nil
	})
	p.funcs = append(p.funcs, onStats)
	p.node.Get("port").Set("onmessage", onStats)
}

// cacheViews keeps the handles the shared-ring hot path touches. The write
// index moves a whole slice at a time and cap is a whole number of slices, so
// one view per slot covers every write.
func (p *Player) cacheViews() {
	p.atomics = js.Global().Get("Atomics")
	n := p.opt.Slice
	p.views = make([]js.Value, p.cap/n)
	for i := range p.views {
		p.views[i] = p.data.Call("subarray", i*n*8, (i+1)*n*8)
	}
}

// target is the queue to keep, in frames. Zero until set means Options.Queue,
// so a Player made by Open or openWorker needs no extra step.
func (p *Player) target() int64 {
	if q := p.queue.Load(); q > 0 {
		return q
	}
	return int64(p.opt.Queue) * int64(p.opt.SampleRate) / int64(time.Second)
}

// SetQueue changes how much audio is kept queued ahead of the speaker, while
// playing: a shorter queue hears a trigger sooner, a longer one survives a
// busier page. Lowering it lets the queue drain down; raising it refills. It
// is held between one slice and the ring less one slice, and the value in
// effect is returned.
func (p *Player) SetQueue(d time.Duration) time.Duration {
	f := int64(d) * int64(p.opt.SampleRate) / int64(time.Second)
	f = max(f, int64(p.opt.Slice))
	if p.cap > 0 {
		f = min(f, int64(p.cap-p.opt.Slice))
	}
	p.queue.Store(f)
	return time.Duration(f) * time.Second / time.Duration(p.opt.SampleRate)
}

// queued counts slices still in flight to the worklet as queued. Topping up
// from the worklet's own fill instead misses them and overfills the ring —
// the likely cause of the irregular artifacts in an earlier bench worklet.
func (p *Player) queued() int64 {
	if p.shared {
		w, r := p.atomics.Call("load", p.ctrl, 0).Int(), p.atomics.Call("load", p.ctrl, 1).Int()
		return int64((w - r + p.cap) % p.cap)
	}
	return p.sent.Load() - p.played.Load()
}

// pump is the producer: render within the budget, then sleep, which in wasm
// hands control back to the page's event loop. While the context is
// suspended (a phone call, a backgrounded tab) nothing is played, the queue
// stays full, and the pump only sleeps.
func (p *Player) pump() {
	for !p.closed.Load() {
		t0 := time.Now()
		for p.queued() < p.target() && time.Since(t0) < p.opt.Budget {
			if err := p.push(); err != nil {
				return // the source ended; the worklet plays out the queue
			}
		}
		if p.queued() < p.target() {
			time.Sleep(time.Millisecond) // behind: yield, come straight back
		} else {
			time.Sleep(8 * time.Millisecond) // about a frame
		}
	}
}

// push renders one slice and hands it over: written into the shared ring, or
// posted. The Go buffers are reused; a posted Float32Array is new each time,
// because transferring it detaches it.
func (p *Player) push() error {
	n := p.opt.Slice
	if len(p.raw) != n*4 {
		p.raw, p.f32 = make([]byte, n*4), make([]byte, n*8)
	}
	raw, f := p.raw, p.f32
	if _, err := io.ReadFull(p.src, raw); err != nil {
		return err
	}
	for i := 0; i < n*2; i++ {
		s := float32(int16(binary.LittleEndian.Uint16(raw[2*i:]))) / 32768
		binary.LittleEndian.PutUint32(f[4*i:], math.Float32bits(s))
	}
	if p.shared {
		w := p.atomics.Call("load", p.ctrl, 0).Int()
		js.CopyBytesToJS(p.views[w/n], f)
		p.atomics.Call("store", p.ctrl, 0, (w+n)%p.cap)
		return nil
	}
	a := js.Global().Get("Float32Array").New(n * 2)
	js.CopyBytesToJS(js.Global().Get("Uint8Array").New(a.Get("buffer")), f)
	p.node.Get("port").Call("postMessage", a, []any{a.Get("buffer")})
	p.sent.Add(int64(n))
	return nil
}

// Underruns counts render quanta the worklet had to pad with silence after
// playback began.
func (p *Player) Underruns() int64 {
	if p.shared {
		return int64(p.atomics.Call("load", p.ctrl, 2).Int())
	}
	return p.underruns.Load()
}

// Queued returns how much audio is waiting ahead of the speaker.
func (p *Player) Queued() time.Duration {
	return time.Duration(p.queued()) * time.Second / time.Duration(p.opt.SampleRate)
}

// Shared reports whether the shared-memory ring is in use.
func (p *Player) Shared() bool { return p.shared }

// Pause suspends output, and with it the source's clock, until Resume. Taps
// on the page don't resume it meanwhile, as they do an OS-suspended context.
func (p *Player) Pause() {
	p.held.Store(true)
	if p.worker {
		p.tell("pause")
		return
	}
	p.ctx.Call("suspend")
}

// Resume restarts output after Pause. On iOS call it from inside a user
// gesture handler, or the context may stay suspended.
func (p *Player) Resume() {
	p.held.Store(false)
	if p.worker {
		p.tell("resume")
		return
	}
	p.ctx.Call("resume")
}

// Close stops the pump and closes the AudioContext.
func (p *Player) Close() {
	p.closeOnce.Do(func() {
		p.closed.Store(true)
		if p.worker {
			p.tell("close")
		} else {
			if !p.node.IsUndefined() && !p.node.IsNull() {
				p.node.Call("disconnect")
			}
			p.ctx.Call("close")
		}
		for _, f := range p.funcs {
			f.Release()
		}
	})
}

// unlockOnGesture resumes the context on each gesture until it runs. The
// listeners stay registered, so a context the OS suspends later (iOS does on
// interruptions, leaving it "interrupted" rather than "suspended") resumes on
// the next tap.
//
// resume() alone is not reliable on iOS Safari for a context created before
// the gesture: the first tap could leave it suspended and only a second tap
// started sound. Starting a one-frame silent source inside the same gesture is
// the long-standing iOS unlock, and makes the first tap count.
func (p *Player) unlockOnGesture() {
	resume := js.FuncOf(func(js.Value, []js.Value) any {
		if p.held.Load() || p.ctx.Get("state").String() == "running" {
			return nil
		}
		p.ctx.Call("resume")
		src := p.ctx.Call("createBufferSource")
		src.Set("buffer", p.ctx.Call("createBuffer", 1, 1, p.opt.SampleRate))
		src.Call("connect", p.ctx.Get("destination"))
		src.Call("start", 0)
		return nil
	})
	p.funcs = append(p.funcs, resume)
	doc := js.Global().Get("document")
	for _, ev := range []string{"touchend", "mouseup", "keyup", "click"} {
		doc.Call("addEventListener", ev, resume)
	}
	p.ctx.Call("resume") // succeeds outright where autoplay is allowed
}

// await blocks the calling goroutine on a JS promise.
func await(promise js.Value) error {
	done := make(chan error, 1)
	var ok, fail js.Func
	ok = js.FuncOf(func(js.Value, []js.Value) any { done <- nil; return nil })
	fail = js.FuncOf(func(_ js.Value, a []js.Value) any {
		done <- errors.New("webaudio: " + a[0].Call("toString").String())
		return nil
	})
	promise.Call("then", ok, fail)
	err := <-done
	ok.Release()
	fail.Release()
	return err
}
