# Vision

undertone gives a game sound from the first commit: ambience that drifts with the
state of the world, and the one-shots every game needs, with no asset library. It
is synthesized end to end, from a seed and a handful of knobs.

## Who it is for

Go game developers. The engine is plain Go and imports nothing from let-go, so any
Go game can use it. let-go is the first-class way in: the `audio` namespace needs no
generated interop, the mood derivation is written in lg as well as Go, and moods are
data a game can bring and change.

## Where it sits

| Layer | Owns |
|---|---|
| let-go's audio seam | getting samples to the speakers, natively and on the web; the write-only rule |
| undertone | the synth, the sequencer, moods, one-shots, the knobs, the lg binding |
| the game | turning its world into knobs and events (tension from health and nearby enemies) |

undertone carries its own output today, an oto player and a WebAudio worklet,
because let-go has no audio seam yet. Three consumers now play through it
(undertone's own hosts, tesserae, and xsofy's lane), which is enough to design
that seam from use, and undertone will propose it. When let-go has one, undertone
plugs into it and drops its own player.

## What it does

- **Ambience that drifts.** A mood is a palette of patches and a set of lines in
  level, and tension works the same way in every mood: tempo up, a pulse fading in,
  the harmony souring, the calm layers thinning, and the drums pushing harder. A
  game moves the knobs and the music follows, never the same twice.
- **One-shots.** Footsteps, hits, pickups and the rest, pitched in the music's key
  when they should be, from a small voice pool that never disturbs the score. A game
  can register its own.
- **Music, two ways.** Adaptive is the default. Authored is for when a game wants a
  particular tune, such as a title theme, a boss phrase or a jingle. Both use the
  same synth and patches, so they sound like one game.
- **Room to grow.** The synth and sequencer underneath are general enough for music
  and sound design beyond games, and may become more modular over time. Today the
  base is ambience and sfx.

## Live coding

Supported, not the headline. The control surface merges partial maps at the next
pattern boundary, so a let-go REPL can reshape the music as it plays, and that stays
true. undertone is not becoming a live-coding environment; its main user is a game,
not a performer.

## Not undertone

- **A DAW.** The designer and the grid stay small: they shape moods and patterns
  for a game, not songs for release.
- **Samples or an asset pipeline**, for now. Every sound is synthesized.
- **Positional audio**, for now. Stereo pan only.

## Decided

As of 2026-10-18:

- **xsofy is the target.** It needs a handful of moods, tension driving them,
  one-shots, and authored themes and an outro. tesserae and phosphene are the lab
  that prepares for it.
- **Moods are data.** A mood is a set of lines in level; tension works the same in
  every mood, so a game brings or edits lines, not code. One tension drives one
  mood at a time.
- **Authored music both replaces and layers.** Replacing comes first: a title
  theme, an outro. A grid already does both for one pattern (`Bed` keeps the
  generated drone and pad underneath); a theme needs several patterns, a way to
  play once and hand back or stop, and a way in from lg.
- **A mood change is heard at once.** A designer tuning a mood, or a game switching
  scenes, hears the change within about one output buffer, not at the next
  pattern.
- **undertone proposes let-go's audio seam.** It brings output; phosphene brings
  input and a playback clock to sync against.
- **Structure follows need.** The layers (knobs, sequencer, render, output) are a
  way to think about undertone, not a list of types to build. A layer becomes its
  own type when a second user, duplicated knowledge, or caller complexity calls
  for it.

## Open

- Whether an authored piece still answers to tension.

## Where it came from

It grew out of xsofy's search for an audio seam: a GL spike led back to ebiten, and
ebiten to oto, a Go audio output that works natively and in wasm. At about the same
time overtone and sointu came into my awareness. undertone took sointu's shape (a tiny
per-sample VM of synth units, driven by a tracker) and overtone's split: lg is the
control language, and Go makes the samples.
