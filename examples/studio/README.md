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

A click on a layer's name under the meters mutes that layer, and another
brings it back.

The THEME section holds an authored theme as the map `undertone/play-theme`
takes, `{:grids [{:lead [...] :bass [...] :pulse [...] :drums [...] :bed true}]
:then :generate}`. It opens on the xsofy title's notes, over whatever bed is
set; **load xsofy title with its game bed** also sets the mood, level, tension
and seed the game stages for its title screen, which fix the tempo, key and
lead instrument. Edit it or paste one in,
a `(def ...)` from a game's source too, and it applies after a pause; copy takes
it back out. Its play button cycles once, loop, stop, as ♫ does, and plays over
the bed as it is, so through a lead or palette being edited. **without bed**
plays the theme's lead, bass and drums alone. A theme whose `:then` is
`:silence` ends in that silence for a moment before the bed returns.

`#theme=<JSON>` links a theme into that section, with the bed to play it over:
`{name, grids: [{lead, bass, pulse, drums, bed}], then, mood, level, tension,
seed, mute}`. `lead` and `bass` are 16 digits each, a scale degree per step
with 0 a rest; `pulse` is 16 digits, 0 or 1; `mute` is a list of layer names,
and a seed past 2^53 must be a decimal string. Only `grids` is required, and a
link with a bad value changes nothing. A theme link scrolls to the section.

Other pages can link a sound in: `#shot=<EDN>` (or `lead=`, `palette=`)
imports it, and `&take=<JSON>` adds a button that plays a take, a vector of
`undertone/trigger` maps (`shot`, `note` or `degree` and `octave`, `gain`,
`pan`, `delay-ms`, `send`, `verb`, `seed`, `group`; a seed may be a decimal string, as a full 64-bit one must be). The same take goes to `undertoneStudio.trigger`,
and `undertoneStudio.render(take, secs)` renders it offline over the bed as
interleaved stereo float samples, for a waveform. `measure(take, secs)` returns
the sfx board's numbers for it (hash, peak, envelopes, max LUFS, length; on the
16-bit samples), and `hash(take, secs)` the hash alone, for a cache. All three
build one `engine.Take`, so they always describe the same render.

`main.go` is the page's bridge to the engine (`window.undertoneStudio`); the
unit table in `index.html` mirrors the ranges `engine.UnitSpec` validates. The
stack bars are not a copy: the page asks the engine for them (`stack(units)`,
which is `engine.StackTrace`).
