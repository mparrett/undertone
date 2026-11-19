# studio

Shape a one-shot, a lead or a palette by ear, and take it away as the EDN map
`undertone/register-shot`, `register-lead` or `register-palette` reads.

```sh
just studio        # builds main.wasm, copies wasm_exec.js, serves on :8374
```

The page carries its own guide (the **How to use the studio** panel, open on
a first visit). In short:

1. **play** starts a mood's bed; pick **shot**, **lead** or **palette** and
   open a sound. Clicking a name opens it; a built-in becomes your own copy under a new name on your first edit, and shift-click copies it at once.
2. Edit the units. The bars after each unit show the stack after it runs:
   signal (from an `osc` or `pluck`) or control (from an `env` or `lfo`).
3. Every edit registers the sound, so you hear it at once: a shot on keys
   1–8 (scale degrees of the bed), a lead or palette in the bed's place,
   **solo** to hear it alone. A refused edit names the unit and keeps the last
   good version playing.
4. **download** or **copy** the EDN; **import pasted EDN** brings one back.

In a game:

```clojure
(undertone/register-shot (read-string (slurp "sfx/zap.edn")))
(undertone/trigger {:shot :zap :degree 2})
```

Other pages can link a sound in: `#shot=<EDN>` (or `lead=`, `palette=`)
imports it, and `&take=<JSON>` adds a button that plays a take, a vector of
`undertone/trigger` maps (`shot`, `note` or `degree` and `octave`, `gain`,
`pan`, `delay-ms`, `seed`; a seed may be a decimal string, as a full 64-bit one must be). The same take goes to `undertoneStudio.trigger`,
and `undertoneStudio.render(take, secs)` renders it offline over the bed as
interleaved stereo float samples, for a waveform. `measure(take, secs)` returns
the sfx board's numbers for it (hash, peak, envelopes, max LUFS, length; on the
16-bit samples), and `hash(take, secs)` the hash alone, for a cache. All three
build one `engine.Take`, so they always describe the same render.

`main.go` is the page's bridge to the engine (`window.undertoneStudio`); the
unit table in `index.html` mirrors the ranges `engine.UnitSpec` validates. The
stack bars are not a copy: the page asks the engine for them (`stack(units)`,
which is `engine.StackTrace`).
