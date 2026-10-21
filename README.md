# undertone

A small generative-ambience engine for games, with a binding for
[let-go](https://github.com/nooga/let-go). It is a demo-sized homage to
[vsariola/sointu](https://github.com/vsariola/sointu): there is no sointu code
here, only its shape. Instruments are stacks of unit opcodes run by a tiny
per-sample VM, a tracker-style sequencer drives them, and the whole track is
derived from a seed.

**Try it in a browser: [matt.parrett.us/undertone](https://matt.parrett.us/undertone/)**
(the [grid](https://matt.parrett.us/undertone/grid/), the
[player](https://matt.parrett.us/undertone/player/), and a
[let-go-driven descent](https://matt.parrett.us/undertone/descent/)).

[![The grid example: a step sequencer playing over the engine's bed, with lg mutations and a mood change](docs/grid.gif)](https://matt.parrett.us/undertone/grid-lg/)

The module has these parts:

| Package | What it is |
|---|---|
| `engine` | The synth VM, the sequencer, a one-shot sfx voice pool, and the game-state → parameters derivation. Plain Go; imports nothing from let-go. |
| `letgo` | The let-go binding: an `undertone` namespace that drives the engine from lg code, plus `letgo/lg/ambience.lg`, the same derivation written in lg. |
| `webaudio` | Browser output: an AudioWorklet fed in small slices from the page's event loop (see [In the browser](#in-the-browser)). |
| `cmd/undertone` | A CLI that plays live, renders WAVs, and draws a four-band VU meter. |
| [`examples/`](examples) | A step sequencer, the engine in a web page, and a let-go-driven game demo, each runnable in a browser. |

[VISION.md](VISION.md) says what undertone is for and what it won't be.

[docs/vm.md](docs/vm.md) explains how the synth VM makes a sample and where its limits are.

`cmd/lghost` is a minimal lg runner with the binding linked in, for trying the
namespace without lgx.

## The engine

Music parameters come from game state:

- **mood**: `dungeon` (minor ladder, saw drone, fifth), `overworld`
  (mixolydian → major → lydian with elevation, sine bed, lifted bells),
  `meadow` (major, high and airy: near-silent drone, dense lifted bells),
  `aurora` (slow lydian veils through the delay, no drone, the odd chime),
  `village` (the first with drums: a swung kick, clap and shaker under
  kalimba and pluck, building to a festival with level) or `rust`
  (industrial: detuned saws through swept resonant filters, a hiss bed, a
  tritone, sinking as level rises; under tension a broken beat with ratchets
  comes in on an industrial kit).
  Moods are rows in a table (`engine/mood.go`, mirrored in
  `letgo/lg/ambience.lg`), so adding one is an entry in each
- **level**: depth darkens, slows and thickens; elevation brightens; a mood's `open`
  line can also open or close its resonant filters with level
- **tension** (0..1): raises tempo, fades in a low pulse, slides the second
  drone from fifth to tritone to minor ninth, thins the calm layers, in a
  mood with drums fills in the shaker and hits harder, and sets grit, which
  overdrives and degrades the patches built for it (`rust`'s); a game can
  also set `:grit` on its own
- **seed**: picks from hand-tuned option tables, including the lead
  instrument (bell, chime, clang, kalimba, reed, or a Karplus-Strong pluck)

`Synth.SetParameters` stages a new parameter set and applies it at the next
pattern boundary, so a change mid-playback is a seamless transition. Output is
44.1 kHz 16-bit stereo through a cross-fed delay bus and a tanh soft clip.
Out-of-range parameters are clamped rather than trusted, since they often come
from lg code or a page.

The sequencer is a 16-row tracker underneath. `Synth.SetGrid` hands it a
written pattern (lead and bass lanes of scale degrees, a pulse lane, and
kick, clap and shaker lanes of sixteenths in groove notation) to play in place
of the generated one, optionally over the generated drone, pad and air;
`SeedGrid` derives a starting pattern from a seed, writing out the mood's
groove if it has one. The
[grid example](examples/grid) is a step sequencer built on it.

An authored theme is grids played in order, one per pattern: a title theme,
a boss phrase, an outro. `Synth.PlayTheme` starts one at once from its first
grid, and its `Then` says what follows the last: `ThenGenerate` hands back
to the generator, `ThenSilence` rests until the next theme, and `ThenLoop`
repeats it. `ThemePlaying` reports whether it has still to finish. A theme is
written in scale degrees, so it plays in the key and tempo the params set.

One-shots run beside the sequencer in an 8-voice pool that the sequencer never
sees, so a trigger cannot change the music's random streams. Patches:
`footstep`, `hit`, `thud`, `creak`, `cry`, `sink`, `lift`, `thunk`, `snap`,
`twang`, `pluck`, and for arcade games `blip`, `tink`, `zap`, `fall`. Triggers
take a gain, a pan, a sample-accurate delay, and either a MIDI note or a scale
degree resolved against the current parameters, so a pickup chime lands in the
music's key. A repeat of the same shot at the same note within 30 ms is
dropped, so a cascade of hits in one frame plays once; when the pool is full, a
new shot takes the quietest voice.

A game adds its own shots at runtime with `engine.RegisterShot` or
`undertone/register-shot`. A `ShotSpec` is a patch and a gate as data, and
`ShotByName` gives any built-in in that form to start from. A patch the synth
could not run safely, one that would overrun the VM's stack or let an envelope
diverge, is refused with the reason.

### Your own moods

A game can bring moods of its own without a fork. A mood is data: lines in
level, scales, leads and grooves, naming one of the built-in palettes for its
patches. Start from a built-in, change what differs, and derive parameters
from it as from any mood:

```go
crypt, _ := engine.MoodByName("dungeon")
crypt.Name = "crypt"
crypt.BPM.Base -= 10
crypt.Leads = []string{"clang", "clang"}
p, err := crypt.Derive(seed, level, tension)
if err != nil { // an unknown palette, lead or groove
	return err
}
synth.SetParameters(p)
```

From lg, a mood is a map in the shape of `ambience/moods`, with a `:palette`:

```clojure
(def crypt (-> (get ambience/moods :dungeon)
               (assoc :palette :dungeon :leads [:clang :clang])
               (update-in [:bpm :base] - 10.0)))
(ambience/score-spec! crypt seed level tension)
```

The [designer](examples/designer) shapes one by ear and exports it as that
map (`MoodSpec.EDN`). `engine.PaletteNames`, `LeadNames` and `GrooveNames` list
what a mood can name. Patches themselves stay built-in; a game's own one-shot
sounds go through `undertone/register-shot` (`engine.RegisterShot` from Go).

### Determinism

The same parameters render byte-identical audio **on one CPU architecture**.
The derivation itself is pinned across architectures (its products go through
explicit `float64` conversions, so Go cannot fuse them into FMA on arm64). The
per-sample VM and the pattern builder still contain unfused products, so a
render on arm64 and one on amd64 can differ in the last bit. Compare renders on
the same architecture.

## The CLI

```
go install github.com/mparrett/undertone/cmd/undertone@latest

undertone -play                            # live, endless
undertone -play -depth 5 -tension 0.8      # deeper and more dangerous
undertone -play -mood overworld -depth 3   # -depth -3 is the same thing
undertone -play -vu compact                # meters: bars | compact | tall
undertone -wav out.wav -depth 4 -lead pluck -dur 30
```

## The let-go binding

Importing `github.com/mparrett/undertone/letgo` registers the `undertone`
namespace from the package's `init`, so lg code can call it with no generated
interop. It was `audio` until 2026-10-20; that name is left for let-go's own
audio seam ([nooga/let-go#255](https://github.com/nooga/let-go/issues/255)).

| native | |
|---|---|
| `undertone/set-parameters` | one map, merged over the current state, applied at the next pattern boundary; unknown keys are ignored |
| `undertone/set-parameters-now` | the same merge, heard within one block of 128 frames instead, with short fades so it does not click: for a scene switch, or tension rising |
| `undertone/music-start`, `undertone/music-stop` | playback |
| `undertone/set-music-gain` | the music's level under the one-shots, 0..1 |
| `undertone/play-theme` | `{:grids [{:lead [1 0 3] :bass [1] :pulse [true] :drums ["X..." "" "x.x."] :bed false} ...] :then :generate}`: an authored theme, from its first grid at once; `:then` is `:generate`, `:silence` or `:loop`; nil stops it. Sent before `music-start`, it starts with the music |
| `undertone/theme-playing?` | whether the last theme has still to finish |
| `undertone/trigger` | a trigger map (`{:shot :hit :gain 0.8 :pan 0.3}`) or a vector of them; fire-and-forget |
| `undertone/register-shot` | a game's own one-shot (`{:name :ping :gate-ms 20 :patch [[:env 0.001 0.2 0 0.1] [:osc 0 :sine 0 1] [:mul] [:out 0.5 0.5]]}`); a patch the synth could not run safely is an error |
| `undertone/render-wav` | `(undertone/render-wav "out.wav" 20.0)`: offline render of the current parameters, for checking determinism without a sound card. It returns the PCM's SHA-256 as hex, and a nil path skips the file. An optional script plays calls at given seconds, so a test can pin a theme or an outro by hash: `[[0.0 :play-theme {...}] [4.5 :trigger {:shot :hit}] [8.0 :set-parameters-now {:mood :rust}]]`, with `:set-parameters`, `:set-parameters-now`, `:play-theme` and `:trigger` taking what the natives take |

The surface is write-only: nothing reads audio state back into lg, so game
logic cannot branch on whether a sound played and replays stay deterministic.
Triggers sent before the music starts are dropped silently.
A theme sent before the music starts waits for it, but it is checked at
once, so a game's tests can check its themes with no audio. A lead or bass
degree outside 0..8, a lead, bass or pulse lane longer than 16 steps, or more
than three drum lanes is an error from `play-theme`. Drum strings are not
checked: they run to 32 sixteenths, and a character other than `X`, `x` or
`o` plays as a rest.

The natives act on a default `letgo.Music`. A Go host that wants to own the
instance creates one with `letgo.NewMusic` and calls `letgo.SetMusic` before
running lg code. Importing the package also registers `undertone.ambience`,
the lg derivation, so `(require '[undertone.ambience :as ambience])` resolves in
any binary that links it, with no source path: a custom `lg`, a `cli.Main`
host, a browser build. A Go host can call `letgo.LoadEmbedded`, which requires
it as `ambience` in the host's namespace.

### With lgx

Link the binding into a built `lg` runtime as a Go dep. It registers itself,
so it needs `:go/version` and no `:go/interop`:

```clojure
{:lg-runtime :built
 :lg-version "1.13.0"
 :deps {github.com/mparrett/undertone/letgo {:go/version "main"}}}
```

`:go/version` takes anything Go accepts: a tag, a commit, a branch. Then, from
lg:

```clojure
(undertone/set-parameters {:mood :dungeon :bpm 54.0 :root 38 :drone-gain 0.5})
(undertone/music-start)
(undertone/trigger {:shot :footstep :gain 0.3 :pan 0.4})
```

The `undertone.ambience` namespace that derives full parameter maps from
`(seed, depth, tension)` comes with the import, as above; its source is
`letgo/lg/ambience.lg`.

### Without lgx

```
go run ./cmd/lghost -e '(do (ambience/score! 7 4 0.6) (undertone/music-start) (sleep 60000))'
go run ./cmd/lghost -e '(do (ambience/score! 7 4 0.6) (undertone/render-wav "out.wav" 20.0))'
```

`letgo/parity_test.go` renders every mood across a grid of seeds, depths and tensions
through both derivations, Go and lg, and compares the parameters field by
field plus a hash of the PCM. Two things it caught are worth knowing if you
port similar code to lg. First, lg's `*` raises on overflow, so seed mixing
needs `unchecked-multiply`. Second, lg has no unsigned integer, so an unsigned
remainder needs doing by hand (`ambience/umod`).

### A full lg CLI with audio

A program that already runs on the stock `lg` CLI, with its flags (`-source-paths`,
`-storage-id`, …), can keep them all by building that CLI with the binding linked
in. The whole `main.go` is:

```go
package main

import (
	"os"

	"github.com/nooga/let-go/pkg/cli"

	"github.com/mparrett/undertone/letgo"
)

func main() {
	_ = letgo.CurrentMusic() // the import registers the undertone namespace
	os.Exit(cli.Main("dev", "undertone"))
}
```

Run the program with this binary in place of `lg`. The import also registers
`undertone.ambience`, so a program that uses it requires
`[undertone.ambience :as ambience]` with no source path. To keep one codebase that also runs on a
stock `lg`, where there is no `undertone` namespace, look the natives up at runtime:
`(resolve 'undertone/trigger)` is nil there.

## Native playback

Natively, `letgo.Music` and `cmd/undertone` play through the `nativeaudio`
package, which wraps [oto](https://github.com/ebitengine/oto) and has the same
`Open`, `Pause`, `Resume` and `Close` as `webaudio`. A Go game can call
`nativeaudio.Open(synth, nativeaudio.Options{SampleRate: engine.SampleRate})`
itself. The package keeps oto's rules in one place:

- **One context per process.** It is opened once and shared, so a player
  closed and opened again reuses it.
- **Keep the player reachable.** oto attaches a cleanup to every `*oto.Player`
  that closes it once the handle is unreachable, and its internal mixer does
  not keep the handle alive
  ([ebitengine/oto#293](https://github.com/ebitengine/oto/issues/293)). A
  player held only in a local stops at the next GC, silently, with no error.
  `nativeaudio.Player` keeps it in a field, and
  `cmd/undertone/playerlifetime_test.go` parses every `NewPlayer` call site in
  the module and fails on one that is neither stored in a field nor kept
  alive.
- **Short buffers.** It opens oto with a 512-frame device buffer and a 40 ms
  read-ahead in place of oto's streaming-oriented defaults
  (`nativeaudio/latency.go`). On the measuring bench, tap-to-sound dropped
  from a median of about 820 ms to about 130 ms. `Options.Queue` sets the
  read-ahead: the CLI asks for 500 ms, since a bed alone has no one-shots
  waiting behind it and the slack survives a slow terminal.

## In the browser

`letgo.Music` and `cmd/undertone` play through the `webaudio` package under
`GOOS=js`, not oto. oto's browser backend renders a whole buffer inside the
audio thread's refill deadline, and Go's wasm has one thread with no
preemption, so on a phone the burst overran the deadline and crackled at every
buffer size tried. `webaudio` decouples the two: an AudioWorklet only copies
out of a ring, and a goroutine renders 512-frame slices within a 3 ms budget
until about 300 ms is queued, then yields to the page. Where the page is
cross-origin isolated the ring is a SharedArrayBuffer; elsewhere slices travel
as transferred messages. Both played without a dropout on an iPhone, including
under 120 ms main-thread stalls.

Two things the page has to provide: HTTPS (or `localhost`), because Safari
offers AudioWorklet only in a secure context; and a tap, because browsers
start audio only from a user gesture. `webaudio.Open` listens for that tap
itself.

`webaudio.Open` also works inside a Web Worker, which is where `lg -w` runs a
let-go program. The worker has no AudioContext, so it renders into the shared
ring and posts it to the page, where `webaudio/page.js` plays it. Load that
script before the runtime's own: it wraps the `Worker` constructor to listen on
every worker the page creates. Worker mode needs a cross-origin isolated page.

In worker mode, a program that blocks on input silences the music. let-go's
`term/read-key` under `lg -w` waits on the key ring with no timeout, which
parks the whole worker, so the goroutine that renders stops until a key
arrives ([nooga/let-go#1086](https://github.com/nooga/let-go/issues/1086)).
Until that is fixed, a prompt that should keep the music playing can poll
`term/key-pending?` with a short `sleep` between checks, which lets the
renderer run.

let-go itself may grow a host audio seam; see
[nooga/let-go#255](https://github.com/nooga/let-go/issues/255).

## Building

Builds need no cgo. `CGO_ENABLED=0` works for macOS, for Linux (PulseAudio
through a pure-Go client, or ALSA loaded through purego), and for
`GOOS=js GOARCH=wasm`. oto is pinned at v3.5.1 and is used only natively.
The let-go binding avoids reflection-boxed natives, so it also runs under
TinyGo.

## History

Extracted on 2026-10-09 from a private workspace, where it began as
`sointu-homage` on 2026-08-14. The commit history is preserved.

## Thanks

To [jasalt](https://github.com/jasalt), whose
[govertone](https://github.com/jasalt/govertone), an experiment pairing Sointu
with let-go shared in the Clojurians Slack `#let-go` channel in July 2026,
introduced Sointu to this project and prompted it. And to
[vsariola](https://github.com/vsariola) for Sointu itself.

## License

MIT; see [LICENSE](LICENSE).
