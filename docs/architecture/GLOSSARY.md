# Glossary

The terms on the layer diagrams in `current/` and `decided/`, plus the engine's
working vocabulary. `engine/` is the source of truth for anything in code font.

## Game-facing

| term | meaning |
|---|---|
| **let-go**, **lg** | The Clojure-like language games script in. undertone registers an `undertone` namespace and `undertone.ambience` with it. |
| **mood** | A named piece of ambience (`dungeon`, `rust`, …): a palette plus a set of lines in level. Moods are data a game can bring. |
| **level** | Where the player is, as an integer: depth, elevation, the climb through a meadow. Each mood decides what it means. |
| **tension** | What is happening, 0..1. It works the same in every mood: tempo up, the pulse in, the harmony souring, the calm layers thinning, and the drums harder. |
| **mood line** | One parameter's response to level: base + step × level, clamped (`line` in `engine/mood.go`). |
| **derive**, **derivation** | Turning (mood, seed, level, tension) into `Params` (`DeriveMood`). This is the only place level and tension exist. Downstream sees only knobs. |
| **one-shot**, **shot** | A short sound fired by an event, such as a footstep or a hit, optionally pitched in the music's key (`Trigger`). A game can add its own through `ShotSpec`. |
| **specs** | The data a game brings instead of Go code: `MoodSpec` for a mood, `PaletteSpec` and `LeadSpec` for a patch bank and a lead, `ShotSpec` for a one-shot. Each is validated before it can reach the audio thread. |

## Control

| term | meaning |
|---|---|
| **Params**, **knobs** | The one struct that drives the music: tempo, key, track gains and densities, palette, drums, grit, open and mute. It holds no level or tension. |
| **SetParameters** | Stages `Params` for the next pattern boundary, about 7 s at 66 BPM. lg's `set-parameters` calls it. |
| **SetParametersNow** | Applies `Params` within one block of 128 frames: the rest of the pattern is redrawn and each voice fades to its new note over about 5 ms. It drops a change `SetParameters` staged for the boundary, since the newer call is complete. lg's `set-parameters-now` calls it. |
| **glide** | How grit and open reach notes already sounding: they move to the new value over about 0.1 s rather than waiting for the next note. |
| **grit** | 0..1. It pushes `drive`, `crush` and `svf` resonance harder. Moods derive it from tension. |
| **open** | -1..1. It moves every `svf` cutoff up to two octaves. Moods derive it from level. |
| **music gain** | A master level on the music, applied below the one-shots (`SetMusicGain`). |
| **sfx gain** | A master level on the one-shots and their sends, leaving the music alone (`SetSfxGain`). |

## Sequencer

| term | meaning |
|---|---|
| **row** | One step of the sequencer: half a beat. |
| **pattern** | 16 rows, or two bars. Notes are drawn and `SetParameters` lands at pattern boundaries. |
| **track** | One of the six melodic voices the sequencer drives: drone, fifth, pad, lead, air and pulse. There are three drum lanes beside them. |
| **Sequencer** | The layer that decides which note plays on which row. On the diagram it groups the Generator, the Grid and the Arranger. It is a mental model only. In code, it is part of `Synth`. |
| **Generator** | Builds each pattern's notes from `Params` and a seeded rng (`buildPatterns`, `buildDrums`). This is what makes the ambience generated rather than scored. |
| **Grid** | A hand-written pattern played in place of the generator (`SetGrid`). Melodic lanes hold scale degrees, so it stays in key when the mood changes. |
| **Bed** | A `Grid` flag. With it set, the generator keeps playing drone, pad and air under the grid. This is the "layer" case. Without it, only the grid plays. |
| **authored theme** | A tune a game wants played as written: a title theme, a boss phrase or an outro. A `Theme` is a list of grids played in order (`PlayTheme`, from Go or lg), then `ThenGenerate`, `ThenSilence` or `ThenLoop`. |
| **replace / layer** | The two ways authored music meets the generator. Replace means the grid takes those tracks. Layer means a `Bed` underneath. |
| **Arranger** | Deferred (#46). It would merge generated and authored rows once an authored melody must answer to tension. Until then, the grid simply replaces rows. |
| **groove** | A drum pattern in text (`X x o .` plus ratchets), picked by seed from the mood's list. |

## Render

| term | meaning |
|---|---|
| **patch** | A list of units, each an op with up to four parameters. Built-in patches are Go tables; a game's come from its specs, which `buildPatch` checks first: stack depth, ranges, release and gain. |
| **unit** | One step of a patch: an op such as `env`, `osc`, `mul`, `svf` or `out`, plus its parameters. `UnitSpec` in `engine/shotspec.go` lists every op with its stack effect. |
| **stack** | Where units pass values, instead of named wires: a unit pops its inputs and pushes its result. At most eight values, fresh for every sample. A patch reads as postfix, so `env osc mul out` is `out(mul(env, osc))`. |
| **signal**, **control** | The two kinds of value on the stack. A signal is audio, pushed by `osc` or `pluck`. A control is a shape that steers something, pushed by `env` or `lfo`. `buildPatch` tracks which is which, since an offset on a signal would be DC. |
| **unit VM** | Runs a voice's patch once per sample on an eight-float stack, as sointu does. See `docs/vm.md`. |
| **voice** | One running patch: a track, a drum lane or a shot. |
| **palette** | A mood's patch bank: which patch each track uses, plus a drum kit. |
| **kit** | The patches for the three drum lanes, either village or industrial. |
| **shot pool** | Eight voices reserved for one-shots, mixed above the music gain, so the shots never steal the music's voices. A new shot takes a free voice, or else steals the quietest. |
| **thinning** | The same shot on the same note can't start again within 30 ms (`shotGapMS`), so a burst of identical triggers doesn't stack into one loud click. |
| **buses** | The shared stereo delay and reverb that `send` and `verb` feed, then a `tanh` on the mix. |
| **`io.Reader`** | `Synth.Read` fills interleaved PCM. It is the one output boundary, and every host reads through it. |
| **layer levels** | A peak meter on each of the seven layers (the six tracks and the drums), read with `LayerLevels`, so a host can show what is sounding. |
| **take** | A render of shots over a bed from a fresh synth, what the sfx studio and board audition (`Take`). Its samples are a function of its fields and `RenderVersion`, and `Take.Hash` digests exactly those. |

## Output

| term | meaning |
|---|---|
| **oto** | The Go audio library undertone plays through natively. It needs one context per process. |
| **nativeaudio** | The desktop player: plays an `io.Reader` through oto, with the same `Open`, `Pause`, `Resume` and `Close` as `webaudio`. It keeps oto's rules (one context per process, a player kept reachable while it plays) so callers needn't. |
| **webaudio** | The browser host: an AudioWorklet reads samples from a ring buffer that wasm fills. In **worker mode** the synth runs in a Web Worker and the page plays the ring on the main thread. |
| **let-go audio seam** | let-go's own audio output (`rt.Audio`, nooga/let-go#255). An lg host built with `-tags audio_oto` streams the music into it, sharing the device with the game's own `audio/tone` and `audio/sample` sounds; without the tag, undertone plays through `nativeaudio`. |

## Signal and timing

| term | meaning |
|---|---|
| **sample** | One number for where the speaker should be at one instant, and the slot it is held for: 1/44,100 s, about 22.7 µs (`SampleRate`). Producing one is one pass of the engine: every voice runs its patch once, then the buses and the `tanh`. |
| **frame** | One stereo pair of samples; `Read` writes four bytes per frame. |
| **block** | 128 frames, about 2.9 ms. `SetParametersNow` and `PlayTheme` are picked up on block boundaries (`nowBlock`). |
| **control rate** | Every 16 samples. `grit` and `open` glide a step, and the units they scale recompute their constants, so that work isn't done per sample. |
| **render ahead** | The engine computes samples in batches, far faster than real time, and the host plays them out at the steady rate. A sample's computation doesn't happen during its own 22.7 µs. |
