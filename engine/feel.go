package engine

import "math"

// Feel: authored notes played less evenly. A grid's lead and bass lanes hold
// a degree per step and nothing else, so without it every authored note is
// struck alike, and a theme reads as sequenced. With Params.Feel above 0
// each note gets a touch: a level from where it falls in the bar, a tone
// that follows the level, and a small seeded difference from its
// neighbours. Everything is a function of the seed, the lane and the row,
// so a render repeats.
//
// Timing is not part of it yet: every note still starts on its row.

// touch is how one note is struck, as offsets from the patch as written, so
// the zero touch is the patch itself.
type touch struct {
	bright float64 // brightness - 1: upper partials' level and decay, a lowpass's cutoff
	ring   float64 // ring - 1: every envelope's decay
	detune float64 // semitones
}

const (
	laneLead = iota
	laneBass
)

// laneTrack is the track each touched lane plays on.
var laneTrack = [2]int{trackLead, trackBass}

// feelMetric is a note's weight by its eighth of the bar: the downbeat
// strongest, then beat 3, then 2 and 4, the offbeats weakest and the last a
// touch stronger, leaning into the next bar. feelCentre is the weight that
// plays at the level a note has without Feel, so accents rise above it and
// weak beats fall below, rather than everything getting softer.
var feelMetric = [8]float64{1.0, 0.70, 0.82, 0.70, 0.90, 0.70, 0.82, 0.74}

const (
	feelCentre = 0.8
	// At Feel 1 a downbeat is 1.4 times as loud (+2.9 dB) and an offbeat
	// 0.8 (-1.9 dB), before the note's own variation. Smaller steps were
	// tried in the listening mock and could barely be told from none.
	feelDepth    = 2.0
	feelLevelIm  = 0.08 // each note's own level, as a fraction either way
	feelBrightIm = 0.15
	feelRingIm   = 0.12
	feelDetuneIm = 0.06 // semitones: 6 cents
	feelMin      = 0.45
	feelMax      = 1.5
)

// feelRng draws for one authored note: a stream per seed, lane and row of
// the run, as chanceRng is for a drum's chance step, so a note is struck the
// same way each time the same seed reaches it.
func feelRng(seed uint64, lane, at int) *rng {
	r := &rng{(seed*0x9e3779b97f4a7c15 ^ uint64(at)*0xbf58476d1ce4e5b9 ^ uint64(lane+1)*0x94d049bb133111eb) + feelSalt | 1}
	r.next()
	return r
}

const feelSalt = 0x66_65_65_6c // "feel"

// feel gives the note applyGrid has just written to lane at row of pat its touch:
// it scales the note's gain and records how it is to be struck. With Feel 0
// it does nothing, and the note's gain is the float it always was.
func (s *Synth) feel(lane, pat, row int) {
	f := s.params.Feel
	if f == 0 {
		return
	}
	r := feelRng(s.params.Seed, lane, pat*rows+row)
	u := func() float64 { return 2*r.f() - 1 } // -1..1
	level := 1 + f*feelDepth*(feelMetric[row%8]-feelCentre)
	level *= 1 + f*feelLevelIm*u()
	level = max(feelMin, min(level, feelMax))
	// a harder note is brighter and rings longer; the exponent keeps
	// brightness from doubling the level's effect on a filtered voice
	bright := math.Pow(level, 0.7) * (1 + f*feelBrightIm*u())
	ring := (1 + 0.25*(level-1)) * (1 + f*feelRingIm*u())
	s.pats[laneTrack[lane]][row].gain *= level
	s.touch[lane][row] = touch{bright - 1, ring - 1, f * feelDetuneIm * u()}
}

// setTouch hands track t's voice the touch of the note at row, before the
// note is triggered. A track with no authored lane has none.
func (s *Synth) setTouch(t, row int) {
	switch t {
	case trackLead:
		s.voices[t].touch = s.touch[laneLead][row]
	case trackBass:
		s.voices[t].touch = s.touch[laneBass][row]
	}
}

// unit is the factor the touch puts on a unit: an env's decay, a lowpass's
// cutoff, an out's gain. chain counts the outs before the unit, so chain 0
// is the patch's first partial, the fundamental, which brightness leaves
// alone.
//
// The rule is the listening mock's, and it reads a patch by its shape: an
// additive voice (chime, kalimba) gets quieter, shorter upper partials on a
// softer note, and a filtered one (pluck, reed) a lower cutoff. A voice
// with one partial and no filter (bell, clang) has no brightness to move,
// and takes only the level, ring and tuning. An svf is left alone: Open
// already glides its cutoff.
func (t touch) unit(op, chain int) float64 {
	if t == (touch{}) {
		return 1
	}
	b := 1 + t.bright
	switch op {
	case opEnv:
		if chain > 0 {
			return (1 + t.ring) * b
		}
		return 1 + t.ring
	case opLpf:
		return b * b
	case opOut:
		if chain > 0 {
			return b * b
		}
	}
	return 1
}
