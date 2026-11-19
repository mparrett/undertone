# Examples

Four ways in, all live at
[matt.parrett.us/undertone](https://matt.parrett.us/undertone/),
which `.github/workflows/pages.yml` rebuilds on every push to `main`.
Browser pages need HTTPS or `localhost`, since Safari only offers
AudioWorklet in a secure context; tap to start audio.

| Example | What it shows | Size (wasm) |
|---|---|---|
| [`grid/`](grid) | A step sequencer on the engine: lead, bass, pulse and drum lanes over the generated bed, in any of its moods (village, aurora, meadow, overworld, ruins, dungeon). A link carries the pattern. Built with `-tags lg`, it adds a mutate button whose variations are lg code ([`grid/lgmutate/mutate.lg`](grid/lgmutate/mutate.lg)). | ~5 MB; ~28 MB with lg |
| [`player/`](player) | The engine alone, generative, with a four-band meter and query-string controls (`?depth=4&tension=0.6&seed=7&lead=pluck`). | ~3 MB |
| [`descent/`](descent) | let-go driving the engine: lg code derives music parameters and sound-effect triggers from a scripted descent-and-fight, natively (`cli/`) and in the browser (`web/`). | ~29 MB |
| [`designer/`](designer) | A mood designer: start from a built-in mood, shape its tempo, key, layers and drums, and hear each change as you make it at any level and tension. A mix mutes layers one at a time, and the mood exports as an EDN entry that `ambience/derive-spec` plays. | ~5 MB |
| [`studio/`](studio) | A sound studio: open a one-shot, a lead or a palette, edit its units with the stack shown after each, and hear it over a mood's bed (a shot on a scale degree, a lead or palette in the generator's place, solo or in the mix). Every edit registers it; it exports as the EDN map `undertone/register-shot`, `register-lead` or `register-palette` takes, and imports one back. | ~5 MB |

The descent demo's size is let-go's runtime; the engine is a few MB of it.
Served compressed it is about 4.4 MB with brotli.

## Run

From the repository root:

```sh
# grid
GOOS=js GOARCH=wasm go build -o examples/grid/main.wasm ./examples/grid
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/grid/
python3 examples/serve.py 8900 grid
# add -tags lg to the build for the lg mutate button

# player: the page runs cmd/undertone
GOOS=js GOARCH=wasm go build -o examples/player/main.wasm ./cmd/undertone
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/player/
python3 examples/serve.py 8899 player

# designer: builds, copies wasm_exec.js, and serves on :8373
just designer

# studio: the same, on :8374
just studio

# descent, native
go run ./examples/descent/cli -demo -dry   # the tension trace, no sound
go run ./examples/descent/cli -demo        # with sound

# descent, browser
GOOS=js GOARCH=wasm go build -o examples/descent/web/main.wasm ./examples/descent/web
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" examples/descent/web/
python3 examples/serve.py 8901 descent/web
```

`serve.py` binds all interfaces and sends `Cache-Control: no-cache`, so a
phone on the LAN won't run a stale wasm. A phone needs HTTPS to get
AudioWorklet, though; `tailscale serve` is one way to give it that.

## Manual browser check

`just test-wasm` runs the suites under node, but node is not a browser: the
`webaudio` pump, the AudioWorklet and a phone's scheduling only run in a real
one. Before merging a change to the engine's timing (tempo, patterns, drums,
parameter changes), to `webaudio`, or to `letgo`'s output, work through this
list in a desktop browser and on a phone. It takes about ten minutes.

Add `?debug` to any demo's URL (`…/grid/?debug`, or `&debug` after the
player's own query) to show a panel in the corner. It counts **underruns**
(the speaker got silence because the queue ran dry), **over budget** (renders
that took longer than the audio they made), the slowest render, and how full
the queue is. Underruns are the count to trust; the lowest queue can miss a
dip that the page itself caused. **reset** zeroes the counts, so reset before
each step and read the panel after it. The counts are also published as
`window.__undertoneDebug` for the console.

Serve locally (see [Run](#run)) or use the live pages. A phone needs HTTPS:
use the live pages or `tailscale serve`.

| # | Demo | Do | Pass |
|---|---|---|---|
| 1 | player | Tap to start. Play for a minute without touching anything. | 0 underruns, 0 over budget |
| 2 | player | Switch scenes with the links several times, mid-phrase. | The change is heard at once, with no click, gap or stuck note |
| 3 | designer | With drums playing, click through four or five moods under **start from**, quickly. | No dropped beat at the switch, no held note from the old mood |
| 4 | designer | Drag the **bpm** base slider from one end to the other and back while drums play. | Hits stay in time at every tempo; none vanish at the moment of change |
| 5 | grid | Press **play**, enable some drum steps, then toggle **bed** off and on mid-pattern. | The drone and pad stop at the next row, not at the end of the pattern |
| 6 | grid | Switch moods while the lead and bass play. | The lead and bass move to the new key at once, without a click |
| 7 | studio | Audition a short shot ten times as fast as you can tap. | Every tap sounds; no crackle; underruns stay 0 |
| 8 | any | Switch to another tab for ten seconds, then come back. | Audio resumes by itself or on the next tap; any underruns stop once it plays again |
| 9 | any | On a phone: lock the screen for ten seconds, unlock, return to the page. | As 8 |

Steps 1 to 7 should end with 0 underruns and 0 over budget on a desktop. A
phone may count a few underruns at the moment it resumes (steps 8 and 9); a
count that keeps climbing while audio plays is a failure. When a step fails,
note the browser, the device and the panel's numbers in the PR.

## The descent demo's lg

`descent/lg/music.lg` and `descent/lg/sfx.lg` are frozen prototypes of a game's
music and sound vocabulary; `demo.lg` is written against them. Their
maintained successors live in the game that grew out of them, in a different
shape. Don't tune these copies.
