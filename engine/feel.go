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
// Timing is part of it too: a touched note starts a little after its row,
// by an amount that wanders, as a player's does. See late, below.

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
	ms := f * lateMS(s.params.Seed, lane, pat*rows+row)
	// well short of a row, so a lane's note has started before its next steps
	s.lateBy[lane][row] = min(int(ms*SampleRate/1000), s.samplesPerRow/3)
}

// Timing. A row steps on one sample, and every note of it started there:
// the stiffness a listener hears first. A touched note instead starts late,
// by up to about 25 ms at Feel 1, made of:
//
//   - a base, so that the wander below can run early as well as late of where
//     the lane sits;
//   - a wander that carries from note to note, so the line drifts ahead and
//     behind as a player does, where independent jitter only sounds unsteady;
//   - a lean on the offbeat eighths, which a player places late;
//   - the bass a touch behind the lead.
//
// Nothing starts before its row has stepped, so the sequencer needs no
// look-ahead, and what the row log and a host's playhead say is unchanged.
// The cost is that the lead and bass sit behind the bed and the drums by the
// base, on average: under 15 ms at Feel 1. Delaying everything else by the
// same amount would remove that, and is a larger change.
const (
	lateBaseMS    = 8.0
	lateWanderMS  = 8.0 // the wander's limit either way
	lateWanderSD  = 4.4 // ms per unit of the walk, whose own spread is close to 1
	lateOffbeatMS = 5.0
	lateBassMS    = 4.0
	lateCarry     = 0.8 // how much of the wander carries to the next row
	lateTerms     = 9   // rows back the walk is summed over: 0.8^9 is 0.13
)

// lateMS is how late the note of lane at row at of the run starts, in ms, at
// Feel 1. The wander is a mean-reverting walk, each row's 0.8 of the last
// plus fresh noise, written as the sum it unrolls to so that it is a
// function of the place alone: a redraw of a row (applyNow) gets the lateness
// the step gave it, with no state to carry or to lose at a cut.
func lateMS(seed uint64, lane, at int) float64 {
	walk, w := 0.0, 1.0
	for k := range lateTerms {
		r := feelRng(seed, lane+2, at-k) // the lane's timing stream, apart from its tone's
		walk += w * (2*r.f() - 1)
		w *= lateCarry
	}
	ms := lateBaseMS + max(-lateWanderMS, min(walk*lateWanderSD, lateWanderMS))
	if at%2 == 1 {
		ms += lateOffbeatMS
	}
	if lane == laneBass {
		ms += lateBassMS
	}
	return ms
}

// lateNote is an authored note between its row's step and its own start.
type lateNote struct {
	tr       trackRow
	seed     uint64
	left     int // frames to go; 0 is no note waiting
	row, pat int
}

// holdLate keeps tr, which the row has just stepped on track t, to start
// late, if it is an authored note with a lateness. It reports whether it did.
func (s *Synth) holdLate(t int, tr trackRow, seed uint64) bool {
	lane := trackLane(t)
	if lane < 0 || s.lateBy[lane][s.row] == 0 {
		return false
	}
	s.late[lane] = lateNote{tr, seed, s.lateBy[lane][s.row], s.row, s.pat}
	return true
}

// trackLane is the touched lane that plays on track t, or -1.
func trackLane(t int) int {
	switch t {
	case trackLead:
		return laneLead
	case trackBass:
		return laneBass
	}
	return -1
}

// tickLate counts a frame off each waiting note and starts the ones due.
func (s *Synth) tickLate() {
	for lane := range s.late {
		n := &s.late[lane]
		if n.left == 0 {
			continue
		}
		if n.left--; n.left == 0 {
			s.strike(laneTrack[lane], n.tr, n.seed, n.row, n.pat)
		}
	}
}

// strikeLate starts every waiting note now.
func (s *Synth) strikeLate() {
	for lane := range s.late {
		if n := &s.late[lane]; n.left != 0 {
			n.left = 0
			s.strike(laneTrack[lane], n.tr, n.seed, n.row, n.pat)
		}
	}
}

// waiting reports whether track t's note at row has yet to start.
func (s *Synth) waiting(t, row int) bool {
	lane := trackLane(t)
	return lane >= 0 && s.late[lane].left != 0 && s.late[lane].row == row
}

// redrawLate gives each waiting note the redrawn pattern's version of
// itself, after applyNow: a new level or voice, or no note at all.
func (s *Synth) redrawLate() {
	for lane := range s.late {
		n := &s.late[lane]
		if n.left == 0 {
			continue
		}
		n.tr = s.pats[laneTrack[lane]][n.row]
		if n.tr.note == 0 {
			n.left = 0
		}
	}
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
