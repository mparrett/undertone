package engine

import "strings"

// ---- drums: the rhythmic moods' lanes ----
//
// Drums run beside the six melodic tracks rather than as more of them: they
// need 16th notes, where a sequencer row is an eighth, and keeping them apart
// leaves the moods without drums untouched sample for sample.

const nDrums = 3 // kick, clap, shaker

// grooveSalt offsets both drum rng streams from the melodic ones.
const grooveSalt = 0x9700e5

// drumSound is a lane's patch and the note it plays at. The notes matter
// only for the pitched parts: the kick's fundamental and the clap's body.
type drumSound struct {
	patch Patch
	note  int
}

// kit is one sound per lane: kick, backbeat, and the busy top. A palette with
// no kit of its own plays drumKit.
type kit [nDrums]drumSound

var drumKit = kit{{kick, 35}, {clap, 53}, {shaker, 72}}

// sound is lane d's sound in the current palette's kit.
func (s *Synth) sound(d int) drumSound { return s.params.sound(d) }

// sound is lane d's sound in p's palette's kit.
func (p *Params) sound(d int) drumSound {
	if k := p.Palette.Kit[d]; k.patch != nil {
		return k
	}
	return drumKit[d]
}

// groove is two bars of 16th notes per drum, 32 steps, one pattern long. A
// step is a velocity or a chance:
//
//	X accent   x hit   o ghost   . rest
//	? a hit with probability DrumBusy (level and tension fill the shaker in)
//	f a fill hit with probability 0.15 + 0.6*tension
//	r a ratchet: three hits within the 16th, each quieter
//	R a ratchet with probability tension, else a plain hit
//	g a flam: a grace hit at flamGrace of the level, flamLead before the 16th
//	G a flam with probability tension, else a plain hit
//
// A lane that ends in | is polymetric: the steps before the | loop at their
// own length, running on across pattern boundaries, rather than filling one
// pattern. A groove's polymetric lanes count from the synth's first pattern;
// a grid's from the row its run began (see Synth.nextOwner).
type groove [nDrums]string

var grooveLib = map[string]groove{
	// relaxed: kick on one and three with a push into three, clap on the
	// backbeat, a lilting shaker
	"amble": {
		"X.......x.x.....X.......x.....f.",
		"....x.......x.......x.......x.ff",
		"x?o?x?o?x?o?x?o?x?o?x?o?x?o?x?o?",
	},
	// skipping: the kick syncopated against a straight backbeat
	"skip": {
		"X..x..x.....x...X..x..x.......f.",
		"....x.......x.......x.......x.f.",
		"x.o?x.o?x.o?x.o?x.o?x.o?x.o?x.o?",
	},
	// stomping: four on the floor for the festival end of a village
	"stomp": {
		"X...x...X...x...X...x...X...x.ff",
		"....X.......X.......X.......X...",
		"o?x?o?x?o?x?o?x?o?x?o?x?o?x?o?x?",
	},
	// machine: a broken industrial beat, the kick off the grid against a
	// backbeat that stutters, ratchets that tension turns on
	"machine": {
		"X..xX..x..X.X..xX..xX..x..X.X.R.",
		"....X..o....X..R....X..o....X.rR",
		"x?o?x?R?x?o?x?r?x?o?x?R?x?o?x?R?",
	},
}

// drumHit is a lane's two 16ths within one row: on the row, and the off 16th
// that Swing pushes late. Gains, 0 for no hit, how many times each ratchets
// (0 or 1 for one hit), and whether each is a flam.
type drumHit struct {
	on, off         float64
	onN, offN       int
	onFlam, offFlam bool
}

// A flam's grace hit leads it by 25 ms at half its level. Played flams sit
// about 15 to 40 ms apart: much closer and the two fuse into one thick hit,
// much further and they read as two notes (a drag). The lead is in time, not
// rows, as it is a stroke rather than a subdivision; at half the level the
// grace stays an ornament, a ghost note's level under a hit.
const (
	flamFrames = SampleRate * 25 / 1000
	flamGrace  = 0.5
)

// flamLead is how many frames a grace hit leads its 16th: flamFrames, but
// never more than half a 16th, so at any tempo it lands after the 16th before.
func (s *Synth) flamLead() int { return min(flamFrames, s.samplesPerRow/4) }

// laneStep is the step a lane plays at sixteenth i of a pattern, run
// sixteenths into the lane's run. A plain lane is read by i and rests past
// its end; a polymetric one, ending in |, loops at its own length by run.
func laneStep(lane string, i, run int) byte {
	if n := len(lane) - 1; n > 0 && lane[n] == '|' {
		return lane[(run%n+n)%n]
	}
	if i < len(lane) {
		return lane[i]
	}
	return '.'
}

// buildDrums fills one pattern of drum lanes. It draws from its own rng
// stream, so drum chances never shift what the melodic tracks play.
func buildDrums(p Params, pat int) [nDrums][rows]drumHit {
	var out [nDrums][rows]drumHit
	g, ok := grooveLib[p.Groove]
	if !ok || p.DrumGain <= 0 {
		return out
	}
	r := rng{(p.Seed ^ uint64(pat)*0x9e3779b97f4a7c15) + grooveSalt | 1}
	for d := range g {
		for i := 0; i < 2*rows; i++ {
			v, n, flam := stepHit(laneStep(g[d], i, 2*rows*pat+i), p, &r)
			h := &out[d][i/2]
			if i%2 == 0 {
				h.on, h.onN, h.onFlam = v*p.DrumGain, n, flam
			} else {
				h.off, h.offN, h.offFlam = v*p.DrumGain, n, flam
			}
		}
	}
	return out
}

// stepHit is the velocity a groove step plays at, how many times it
// ratchets, and whether it flams, drawing from r for the chance steps. It is
// the one reader of groove notation: the generator and SeedGrid both go
// through it, so they agree on every symbol.
func stepHit(c byte, p Params, r *rng) (float64, int, bool) {
	switch c {
	case '?':
		if r.f() < p.DrumBusy {
			return 0.45, 1, false
		}
	case 'f':
		if r.f() < 0.15+0.6*p.PulseGain {
			return 0.6, 1, false
		}
	case 'r':
		return 0.7, 3, false
	case 'R':
		if r.f() < p.PulseGain {
			return 0.7, 3, false
		}
		return 0.7, 1, false
	case 'g':
		return 0.7, 1, true
	case 'G':
		return 0.7, 1, r.f() < p.PulseGain
	}
	return drumLevel(c), 1, false
}

// drumLevel is the velocity of a fixed step, X, x, o or a flam's g; 0
// otherwise.
func drumLevel(c byte) float64 {
	switch c {
	case 'X':
		return 1
	case 'x', 'g':
		return 0.7
	case 'o':
		return 0.4
	}
	return 0
}

// stepDrums plays the drums' on-row hits and arms the off 16ths.
func (s *Synth) stepDrums() {
	// the off 16th lands Swing of a row past halfway: 0 is straight, 1/6 a
	// triplet shuffle
	half := max(1, int(float64(s.samplesPerRow)*(0.5+s.params.Swing)))
	for d := range s.dvoices {
		v := &s.dvoices[d]
		h := s.drums[d][s.row]
		s.rat[d] = ratchet{}
		if h.on > 0 {
			v.strike(s.sound(d), h.on, s.drumSeed(d))
			span := half
			if h.off > 0 && h.offFlam {
				span = max(1, half-s.flamLead()) // done before the off 16th's grace
			}
			s.rat[d].arm(h.onN, span, h.on, s.drumSeed(d)+2<<32)
		} else if v.active && v.gate {
			v.gate = false
		}
		s.offLeft[d] = 0
		if h.off > 0 {
			s.offLeft[d] = half
			s.offGain[d], s.offN[d] = h.off, h.offN
			// seeded now: by the time it fires, step has moved on to the next row
			s.offSeed[d] = s.drumSeed(d) + 1<<32
		}
		// the on 16th's grace has played already, from flamsAhead
		s.graceLeft[d] = 0
		if h.off > 0 && h.offFlam {
			s.graceLeft[d] = max(1, half-s.flamLead())
			s.graceGain[d], s.graceSeed[d] = h.off*flamGrace, s.drumSeed(d)+4<<32
		}
	}
}

// flamsAhead looks at the on 16ths of the row about to step and reports
// whether any is a flam; with strike it plays their grace hits. frame calls
// it once a row, flamLead frames before the step, so a grace sounds ahead of
// its beat even when the row is the next pattern's or the first of a grid.
func (s *Synth) flamsAhead(strike bool) bool {
	any := false
	for d := range s.dvoices {
		gain, k, ok := s.flamAhead(d)
		if !ok {
			continue
		}
		any = true
		if strike {
			s.dvoices[d].strike(k, gain*flamGrace, s.drumSeed(d)+3<<32)
		}
	}
	return any
}

// flamAhead reports whether lane d's on 16th in the row about to step is a
// flam, with its gain and the sound it will play.
func (s *Synth) flamAhead(d int) (float64, drumSound, bool) {
	p := &s.params
	if n := s.next.Load(); n != nil && s.row == 0 {
		q := n.sanitized() // step applies it before this row
		p = &q
	}
	var h drumHit
	switch g, _, from := s.nextOwner(); {
	case g != nil:
		h = gridHit(g.Drums[d], s.row, s.pat*rows+s.row-from)
	case s.row != 0:
		h = s.drums[d][s.row]
	default: // the generator's next pattern, not drawn yet
		lanes, ok := grooveLib[p.Groove]
		if !ok || !strings.ContainsAny(lanes[d], "gG") {
			return 0, drumSound{}, false
		}
		h = buildDrums(*p, s.pat)[d][0]
	}
	return h.on, p.sound(d), h.on > 0 && h.onFlam
}

// drumSeed seeds lane d's hit on the current row.
func (s *Synth) drumSeed(d int) uint64 {
	return s.params.Seed + uint64(s.pat*rows+s.row)*nDrums + uint64(d)
}

// ratchet re-strikes a lane's hit within its 16th: left frames until the
// next, gap between them, n still to play, each quieter than the last.
type ratchet struct {
	left, gap, n int
	gain         float64
	seed         uint64
}

// arm sets up the rest of an n-hit ratchet over span frames, after the hit
// that started it at gain.
func (r *ratchet) arm(n, span int, gain float64, seed uint64) {
	if n < 2 {
		return
	}
	gap := max(1, span/n)
	// left counts one more than gap: every arm happens in a frame whose
	// runDrums then ticks it, and a re-strike in the hit's own frame would
	// cut the hit off
	*r = ratchet{left: gap + 1, gap: gap, n: n - 1, gain: gain * 0.75, seed: seed}
}

// strike hits a drum lane afresh. trigger's legato path suits drones but not
// drums: a lane that hits every 16th is still active at its next hit, and
// carrying its envelope and phase over would soften every transient and
// start the clap's re-hits mid-burst.
func (v *Voice) strike(k drumSound, gain float64, seed uint64) {
	v.active = false
	v.trigger(k.patch, k.note, 1, gain, seed)
}

// runDrums fires any off 16th that is due and renders the drum voices.
func (s *Synth) runDrums(l, r *float64) {
	for d := range s.dvoices {
		v := &s.dvoices[d]
		if s.graceLeft[d] > 0 {
			if s.graceLeft[d]--; s.graceLeft[d] == 0 {
				v.strike(s.sound(d), s.graceGain[d], s.graceSeed[d])
			}
		}
		if s.offLeft[d] > 0 {
			s.offLeft[d]--
			if s.offLeft[d] == 0 {
				v.strike(s.sound(d), s.offGain[d], s.offSeed[d])
				// the off 16th's ratchet fills what is left of the row, up to
				// the grace of a flam on the next row
				span := s.rowLeft
				if _, _, flam := s.flamAhead(d); s.offN[d] > 1 && flam {
					span = max(1, span-s.flamLead())
				}
				s.rat[d].arm(s.offN[d], span, s.offGain[d], s.offSeed[d]+2<<32)
			}
		}
		if rt := &s.rat[d]; rt.left > 0 {
			if rt.left--; rt.left == 0 {
				v.strike(s.sound(d), rt.gain, rt.seed)
				rt.gain, rt.seed, rt.n = rt.gain*0.75, rt.seed+1, rt.n-1
				if rt.n > 0 {
					rt.left = rt.gap
				}
			}
		}
		if v.active {
			v.run(l, r, &s.bus)
		}
	}
}

// pickGroove draws the groove from its own stream, as pickLead draws the
// lead, so a mood's grooves never change its notes.
func pickGroove(p *Params, names []string) {
	r := rng{p.Seed*0x9e3779b97f4a7c15 + grooveSalt | 1}
	p.Groove = names[r.n(len(names))]
}
