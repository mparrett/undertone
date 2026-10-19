# Glossary

The terms on the layer diagrams in `current/` and `decided/`. `engine/` is the
source of truth for anything in code font.

## Game-facing

| term | meaning |
|---|---|
| **let-go**, **lg** | The Clojure-like language games script in. undertone registers an `audio` namespace and `undertone.ambience` with it. |
| **mood** | A named piece of ambience (`dungeon`, `rust`, …): a palette plus a set of lines in level. Moods are data a game can bring. |
| **level** | Where the player is, as an integer: depth, elevation, the climb through a meadow. Each mood decides what it means. |
| **tension** | What is happening, 0..1. It works the same in every mood: tempo up, the pulse in, the harmony souring, the calm layers thinning, and the drums harder. |
| **mood line** | One parameter's response to level: base + step × level, clamped (`line` in `engine/mood.go`). |
| **derive**, **derivation** | Turning (mood, seed, level, tension) into `Params` (`DeriveMood`). This is the only place level and tension exist. Downstream sees only knobs. |
| **one-shot**, **shot** | A short sound fired by an event, such as a footstep or a hit, optionally pitched in the music's key (`Trigger`). A game can add its own through `ShotSpec`. |

## Control

| term | meaning |
|---|---|
| **Params**, **knobs** | The one struct that drives the music: tempo, key, track gains and densities, palette, drums, grit, open and mute. It holds no level or tension. |
| **SetParameters** | Stages `Params` for the next pattern boundary, about 7 s at 66 BPM. lg's `set-parameters` calls it. |
| **SetParametersNow** | Stages `Params` for the next row, about 0.45 s at 66 BPM. Only the tempo and new notes change, because the rest of the pattern was already drawn. #47 is about making a mood change heard within one output buffer. |
| **glide** | How grit and open reach notes already sounding: they move to the new value over about 0.1 s rather than waiting for the next note. |
| **grit** | 0..1. It pushes `drive`, `crush` and `svf` resonance harder. Moods derive it from tension. |
| **open** | -1..1. It moves every `svf` cutoff up to two octaves. Moods derive it from level. |
| **music gain** | A master level on the music, applied below the one-shots (`SetMusicGain`). |

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
| **authored theme** | A tune a game wants played as written: a title theme, a boss phrase or an outro. #50 extends the grid to sequences that play once, callable from lg. |
| **replace / layer** | The two ways authored music meets the generator. Replace means the grid takes those tracks. Layer means a `Bed` underneath. Today's need is replace. |
| **Arranger** | Deferred (#46). It would merge generated and authored rows once two sources play together (#39). Until then, the grid simply replaces rows. |
| **groove** | A drum pattern in text (`X x o .` plus ratchets), picked by seed from the mood's list. |

## Render

| term | meaning |
|---|---|
| **patch** | A list of units, each an op with up to four parameters. Patches are fixed Go tables. |
| **unit VM** | Runs a voice's patch once per sample on an eight-float stack, as sointu does. See `docs/vm.md`. |
| **voice** | One running patch: a track, a drum lane or a shot. |
| **palette** | A mood's patch bank: which patch each track uses, plus a drum kit. |
| **kit** | The patches for the three drum lanes, either village or industrial. |
| **shot pool** | Eight voices reserved for one-shots, mixed above the music gain, so the shots never steal the music's voices. |
| **buses** | The shared stereo delay and reverb that `send` and `verb` feed, then a `tanh` on the mix. |
| **`io.Reader`** | `Synth.Read` fills interleaved PCM. It is the one output boundary, and every host reads through it. |

## Output

| term | meaning |
|---|---|
| **oto** | The Go audio library undertone plays through natively. It needs one context per process. |
| **Native Play** | #48. It writes the oto setup once, in place of the two copies in `letgo/` and `cmd/undertone/`. |
| **webaudio** | The browser host: an AudioWorklet reads samples from a ring buffer that wasm fills. **Worker mode** (#45) moves the synth off the main thread. |
| **let-go audio seam** | The output layer let-go doesn't have yet (#40). undertone will propose it and plug into it. phosphene adds input and a playback clock to what it must carry. |
