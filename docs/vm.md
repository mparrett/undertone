# The synth VM

How undertone makes a sample, what it cannot do yet, and what it would take to
reach darker, rougher ambience: industrial scores in the vein of *The Downward
Spiral* ("Hurt", "Closer") and *Ghosts I–IV*. This describes the engine as of
2026-10-17; `engine/engine.go` is the source of truth.

## How a sample is made

A **patch** is a list of units, each an op and up to four parameters. A
**voice** runs its patch once per sample, left to right, on a stack of eight
floats, in the style of sointu and 4klang.

| op | parameters | stack |
|---|---|---|
| `env` | attack s, decay s, sustain, release s | push the envelope |
| `osc` | transpose, shape (sine, saw, pulse, noise), sweep semis, sweep decay s | push a sample |
| `pluck` | transpose, feedback | push a Karplus-Strong string |
| `mul` | | pop two, push the product |
| `lpf`, `hpf` | cutoff Hz | one-pole filter on the top |
| `svf` | cutoff Hz, resonance, mode (0 low, 1 band, 2 high), depth octaves | filter the top; a nonzero depth first pops a modulation value |
| `lfo` | rate Hz, shape (0 sine, 1 triangle, 2 sample-and-hold) | push -1..1 |
| `send` | amount | add the top to the delay bus |
| `out` | gain, pan | pop and mix |

In a `ShotSpec`, an `osc` names its shape in `Shape`, and `Args` holds the
other three.

Layering needs no add op: each `env → osc → mul → out` chain mixes on its own,
so a patch with two such chains is two detuned layers (`drone`, `pad`).

A frame runs, in order:

1. the six sequencer tracks (drone, fifth, pad, lead, air, pulse), one voice each;
2. the three drum lanes;
3. the music gain, then the one-shot pool of `nShots` voices, which sits above it;
4. a cross-fed stereo delay, three rows long, fed by every `send`;
5. `tanh(0.8x)` on the mix, the only nonlinearity in the chain.

The **sequencer** builds 16-row patterns from the params at each pattern
boundary, from a seeded rng. Moods turn level and tension into those params
(`mood.go`): tension raises the tempo, fades in the pulse, sours the fifth,
thins the pad and lead, and drives the drums.

## Limits

**Only the svf's cutoff moves within a note.** An `lfo` or `env` can sweep an
`svf`, and the oscillator has its pitch sweep. Nothing else is modulated: no
pitch vibrato, no level tremolo, no pulse width. Generalizing the svf's
modulation input to other ops is the next step if a patch needs it.

**Resonance stops short of self-oscillation.** The `svf` tops out at a Q of
5, so a voice always dies with its envelope; a filter that sings on its own
would need a voice that outlives its env.

**Distortion only happens on the mix.** The final `tanh` is a safety limiter. No
voice can be overdriven, folded, clipped, bit-reduced or sample-held on its own.

**Noise is one color.** `osc noise` is white noise from an LCG. Pink, brown and
crackle (sparse impulses) all need a filter or a new source.

**The only space is one delay.** The delay is three rows long and has no
damping. Its length is set from the tempo at the first `apply` and never
changes, so once tension raises the tempo it no longer lands on the grid. There
is no reverb.

**The sequencer is a fixed grid.** A pattern is 16 rows at two rows per beat,
so two bars. Drums play 16th notes within it, with swing and probability steps;
the melodic tracks fire on rows only. There are no ratchets, polymeters or
off-grid hits, and every pattern is the same length.

**Patches are code.** Built-in patches are Go tables. A game can add one-shots
through `ShotSpec` (validated, fuzzed), but a mood's palette can only use
built-in patches.

## What sointu has that we lack

sointu's unit types (from `patch.go` upstream), grouped by what they would give
us:

| need | sointu units | ours |
|---|---|---|
| modulation | `send` to any parameter of any unit, `oscillator` with `lfo`, `loadval` | `lfo` or `env` into `svf` cutoff |
| resonant filtering | `filter` (state-variable: frequency, resonance, low/band/high) | `svf` |
| grit | `distort` (drive), `hold` (sample-rate reduction), `crush` (bit depth), `clip` | mix `tanh` only |
| space | `delay` (feedback, damp, note tracking, many taps, reverb by combs) | one delay bus |
| richer sources | `oscillator` color, shape, detune, unison; `noise` shape | four shapes |
| dynamics | `compressor`, `belleq` | none |
| routing | `add`, `push`, `pop`, `xch`, `aux`, `receive` | `mul` only |

sointu is a full tracker synth. We need far less of it: the rows marked
modulation, resonant filtering and grit cover most of the gap.

## What the industrial sound needs

| move | where you hear it | what it takes |
|---|---|---|
| a detuned, slowly sweeping drone | the bed under "Hurt" | resonant lowpass with a slow LFO on cutoff |
| a noise bed with a shape | tape hiss and air across *Ghosts* | bandpass noise, LFO on band and level |
| noise played as a drum or texture | the snares and metal hits of "Closer" | driven, filtered noise with fast envelopes |
| an acid-style bass | the bassline under "Closer" | resonant filter with an envelope on cutoff, plus drive |
| decay and damage | throughout | sample hold and bit crush, amount on a knob |
| syncopation and stutter | drum programming everywhere | ratchets and off-grid hits in the grooves |
| a dark room | the long tails of "Hurt" | a damped reverb send |

## Additions

`svf` and `lfo` are in, with the `rust` mood as their first user: resonant
saws swept by slow lfos, a sample-and-hold noise bed and a filter-envelope
pulse. The rest are candidates. Each keeps the VM's shape: data patches, a fixed stack, no allocation on the
audio thread, and validation that `ShotSpec` and the fuzz test can extend.

1. **`svf`: a state-variable filter** (done). Cutoff, resonance, mode (low, band,
   high) and a modulation depth in octaves, which fills the unit's four
   parameters. It uses the trapezoidal form from Zavalishin and Simper below,
   which stays stable under fast cutoff changes. With a nonzero depth it pops a
   value `m` from the stack and runs at `cutoff × 2^(m × depth)` Hz, so an `env`
   or `lfo` pushed before it becomes a filter envelope or a sweep.
2. **`lfo`: a slow source** (done, in Hz; rows would keep it on the grid). Shape
   sine, triangle or sample-and-hold. It pushes a value in -1..1. Random steps
   from sample-and-hold are a staple of the industrial sound.
3. **`drive`: a waveshaper.** `tanh(k·x)` with a bias for even harmonics.
4. **`crush`: hold and bit depth.** Two parameters, cheap.
5. **A reverb bus** beside the delay: a damped Schroeder/Freeverb-style network
   first, a Dattorro plate if that falls short. It is the largest of these
   additions and the one most likely to show up in a wasm profile.
6. **Groove ratchets and off-grid hits**, as groove syntax, so syncopation stays
   data.

The cost to watch is per-sample math. A fixed `svf` computes its coefficient
once per note, as `lpf` does; a swept one recomputes it every 16 samples. On
2026-10-17 that rendered `rust` at about the cost of `dungeon` natively, where a
coefficient every sample doubled it. The new ops left the golden renders of the
other moods byte-identical.

## Fitting the knobs

Patches stay fixed tables; the knobs scale them at run time, as `v.gain` already
scales every `out` and `send`. Two voice-level scalars would cover most of the
industrial range:

- **grit**, driven by tension: scales `drive`, `crush` and `svf` resonance, and
  the level of noise layers. A calm mood with no gritty units ignores it.
- **open**, driven by level: scales `svf` cutoff, so a rising level opens the
  filters instead of only adding layers.

An industrial mood is then a palette that uses the new ops and a set of lines
like any other mood. The tension story stays the same and only sounds harsher.

## Sources

- Gordon Reid, [*Synth Secrets*](https://www.soundonsound.com/series/synth-secrets-sound-sound)
  (Sound On Sound, 1999–2004): subtractive synthesis from first principles, and
  recipes for drums, bells, strings and more out of oscillators, filters and
  envelopes.
- Andy Farnell, *Designing Sound* (MIT Press, 2010): procedural sound design,
  every sound from code and nothing sampled, as undertone works.
- Miller Puckette, [*The Theory and Technique of Electronic Music*](http://msp.ucsd.edu/techniques.htm):
  free; modulation, waveshaping, filters and delay-based effects with the math.
- Vadim Zavalishin, *The Art of VA Filter Design* (Native Instruments, free
  PDF), and Andrew Simper's [Cytomic technical papers](https://cytomic.com/technical-papers/):
  the trapezoidal SVF that suits `svf`.
- Julius O. Smith, [online books at CCRMA](https://ccrma.stanford.edu/~jos/):
  digital filters, and Schroeder and Freeverb reverbs in *Physical Audio Signal
  Processing*.
- Jon Dattorro, "Effect Design, Part 1" (JAES, 1997): the plate reverb most small
  engines copy.
- [sointu](https://github.com/vsariola/sointu): the closest ancestor, with an
  opcode set that shows what a tiny engine found worth adding.
- On the records themselves, interviews give technique rather than recipes:
  MusicTech's [Landmark Productions: *The Downward Spiral*](https://www.musictech.com/guides/essential-guide/nine-inch-nails-the-downward-spiral/)
  covers the samplers, drum machines and computer-built loops.
