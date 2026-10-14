# Consumer notes: tesserae

Notes from wiring undertone into [tesserae](https://github.com/mparrett/tesserae), a breakout
game in let-go, on 2026-10-14. It's the first consumer that isn't a dungeon game. The spike is
on tesserae branch `spike/undertone-audio`, and the engine changes are on undertone branch
`spike/arcade-shots`.

## What worked unchanged

- **A full `lg` with audio takes three lines.** Blank-import `letgo` and call
  `cli.Main(version, commit)`. The game keeps every lg CLI flag (`-storage-id`,
  `-source-paths`), so its launcher only had to swap the binary. This recipe belongs in the
  README next to lghost.
- **Feature detection from lg is clean.** `(resolve 'audio/trigger)` returns nil on a stock lg,
  so a game can ship one codebase that is silent on stock lg, in sims and in the browser.
- **The write-only surface** meant autoplay and the sims needed no changes.
- **Degree-pitched shots** are the best part for an arcade game. A combo of tile breaks climbs
  the current mood's scale, so it sounds in key with whatever mood is playing.
- It built against let-go `a13e042`, which is newer than the `v1.13.0` in go.mod, with no
  changes.

## Gaps that made it bespoke

1. **The one-shot vocabulary is a fixed Go map.** A game that needs other sounds has to fork
   the engine. This branch adds `blip`, `tink`, `zap` and `fall` as game-agnostic arcade
   shots, but the general fix is a `ShotSpec` (patch plus gate as data, registered at runtime)
   that works the way `MoodSpec` does for moods.
2. **Mood derivation exists only in lg.** A native `:mood` swaps the palette and nothing else,
   so a consumer has to put `letgo/lg` on its search path and `require 'ambience` at runtime.
   `LoadEmbedded` needs an `*api.LetGo`, and a `cli.Main` host never gets one. Options: an
   `audio/score-mood` native that derives in Go, or a hook for registering embedded sources
   that `cli.Main` hosts can use.
3. **`ambience` shadowed `clojure.core/derive`.** Loading it printed a warning on stdout, on
   top of a full-screen terminal UI. Fixed on this branch with `:refer-clojure :exclude`.
4. **No rate limiting or voice stealing policy is documented.** A breakout cascade can fire a
   dozen hits in one frame against 8 voices, so tesserae rate-limits per shot name on its own
   side. An engine-side "same shot within N ms" option, or a documented stealing order, would
   save every action game from writing the same thing.
5. **Latency for hits during play is not yet judged.** The ~130 ms tuned in
   `engine/latency.go` is fine for clears and losses. Whether it is fine for paddle bounces
   needs an ear in play.
6. **There was no music-against-sfx balance.** Every Param shapes the score, so nothing could
   turn the bed down under the one-shots. Playtesting asked for exactly that in the first
   session. Resolved by #5: `Synth.SetMusicGain` and `audio/set-music-gain`, a mixer control
   that leaves the derivation alone. At 1 the render is byte-identical.
7. **The moods have too little range for an arcade game.** All five are ambient beds, and
   percussion exists only inside `village`. tesserae has eight chapter themes and only a couple
   map cleanly: aurora for the opening, dungeon for the heart. The rest were the nearest fit,
   and only chapter III, mapped to village, has drums. What would have helped, in the direction
   of the `MoodSpec` work on `designer-spike` (moods as data that a consumer brings):
   - **Percussion as a property any mood can have,** not a mood of its own. A groove, drum gain
     and busy setting that respond to level and tension, the way BPM and pads already do.
     Merging `:groove` over another mood with `set-parameters` works today, but the derivation
     doesn't know about it, so tension doesn't move it.
   - **Moods a consumer can add from lg.** Today a mood is a row in `engine/mood.go` mirrored
     in `ambience.lg`, and a patch bank only Go can name. A `MoodSpec` registered at runtime
     that names a built-in palette would cover most of what tesserae wanted without a fork.
   - **Wider settings at the ends:** faster and brighter (a boss, a rally) and sparser (a
     puzzle level like the Orrery). Today tension mostly adds BPM and a pulse.
