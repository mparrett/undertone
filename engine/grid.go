package engine

import "sync/atomic"

// Steps is the length of a grid: one pattern, 16 eighth-note rows.
const Steps = rows

// Degrees is the number of pitch rows per melodic lane: a seven-note scale
// plus its octave. A degree is the nth tone of whatever scale is playing, so
// in a five-note mood the 8 rows climb about 1.6 octaves instead.
const Degrees = 8

// DrumLanes is the number of drum lanes: kick, clap, shaker.
const DrumLanes = nDrums

// Grid is a hand-written pattern the synth plays in place of its generator.
// Melodic lanes hold a scale degree per step (1..Degrees, 0 = rest), so a
// grid stays in key when the mood or root changes. Lead and bass replace the
// generator's lead and fifth voices; pulse replaces its tension pulse. Drums
// replace the generator's drums, one lane each for kick, clap and shaker, in
// groove notation: 2*Steps sixteenths of X (accent), x (hit), o (ghost), g
// (flam) or . (rest); a short lane rests for the remainder, and a lane ending
// in | loops at its own length instead (polymetric). With Bed set, the
// generator keeps playing drone, pad and air underneath.
//
// Degree n is the scale's nth tone, wrapping into the next octave: degree 6
// is the octave in a five-note scale, and degree 8 in a seven-note one.
type Grid struct {
	Lead  [Steps]int
	Bass  [Steps]int
	Pulse [Steps]bool
	Drums [DrumLanes]string
	Bed   bool
}

const (
	trackFifth = 1 // voice the bass lane takes over
	trackLead  = 3
	trackPulse = 5
)

// SetGrid makes the synth play g from the next row on; nil hands control back
// to the generator. Edits land at the next row rather than the next pattern,
// so a change is heard the next time the playhead passes it. Safe to call
// from any goroutine.
func (s *Synth) SetGrid(g *Grid) {
	if g != nil {
		c := *g // the caller may keep editing its copy
		g = &c
	}
	s.grid.Store(g)
}

// degreeNote is the note degree d (1-based) lands on above base.
func degreeNote(p Params, base, d int) int {
	return base + scaleTone(p.Scale, d-1)
}

// scaleTone is the semitone offset of tone i (0-based) of scale, wrapping by
// octave past either end: in a five-note scale tone 5 is the root an octave
// up, and tone -2 the fourth tone an octave down. Degrees count the scale's
// own tones (ADR-001), so a five-note mood covers more pitch per degree.
func scaleTone(scale []int, i int) int {
	n := len(scale)
	oct, k := i/n, i%n
	if k < 0 {
		oct, k = oct-1, k+n
	}
	return 12*oct + scale[k]
}

// GridBases is the note a grid's row 1 plays in the lead and bass lanes. It
// puts the lead in a bright middle register whatever the mood's root:
// generator roots run from dungeon-low to overworld-high, and a grid written
// in one should still sit right after switching to the other.
func GridBases(p Params) (lead, bass int) {
	lead = p.Root
	for lead < 67 {
		lead += 12
	}
	for lead > 78 {
		lead -= 12
	}
	return lead, lead - 24
}

// Gains for the grid's lanes, set by measurement (rms while sounding, at
// 110 bpm, as of 2026-10-10): lead about -16 dB, bass -20, pulse and the bed
// -22, so the lead sits a few dB above the rest. The lead voices differ by up
// to 20 dB at equal gain (pluck is the quiet one), hence one gain per voice.
var gridLeadGain = map[string]float64{
	"bell": 0.79, "chime": 0.94, "clang": 1.51, "kalimba": 1.55, "reed": 1.84, "pluck": 7.8,
}

const (
	gridBassGain  = 3.4  // pluck two octaves down: a string bass
	gridPulseGain = 1.1  //
	gridBedScale  = 0.75 // the generator's drone, pad and air, under a grid
	gridDrumGain  = 0.8  // a seeded village groove, about -22 dB: level with the bed (2026-10-12)
)

var gridBass = pluck

// applyGrid overwrites this row of the generated pattern with the grid's.
func (s *Synth) applyGrid(g *Grid, row int) {
	p := s.params
	lead, bass := GridBases(p)
	for _, t := range []int{0, 2, 4} {
		if g.Bed {
			s.pats[t][row].gain *= gridBedScale
			continue
		}
		s.pats[t][row] = trackRow{}
		if row == 0 {
			// a held drone or pad from before the bed went off
			s.voices[t].gate = false
		}
	}
	for d, lane := range g.Drums {
		s.drums[d][row] = gridHit(lane, row, s.pat*rows+row-s.runFrom)
	}
	s.pats[trackLead][row] = trackRow{}
	if d := g.Lead[row]; d > 0 {
		gain, ok := gridLeadGain[p.LeadName]
		if !ok {
			gain = 0.55 // a voice added without measuring it
		}
		s.pats[trackLead][row] = trackRow{degreeNote(p, lead, d), gain, p.LeadGate, p.Palette.Bell}
	}
	s.pats[trackFifth][row] = trackRow{}
	if d := g.Bass[row]; d > 0 {
		s.pats[trackFifth][row] = trackRow{degreeNote(p, bass, d), gridBassGain, 2, gridBass}
	}
	s.pats[trackPulse][row] = trackRow{}
	if g.Pulse[row] {
		s.pats[trackPulse][row] = trackRow{bass - 12, gridPulseGain, 1, p.Palette.Pulse}
	}
}

// SeedGrid derives a starting grid from params, in the generator's manner:
// the lead walks the scale by small steps at the params' lead density, the
// bass marks the root and fifth on the strong beats, the pulse is a
// heartbeat. Deterministic in p.Seed, so a seed is a shareable starting
// point.
func SeedGrid(p Params) Grid {
	r := rng{p.Seed*0x9e3779b97f4a7c15 | 1}
	g := Grid{Bed: true}
	density := max(0.35, min(p.BellDensity*2, 0.7))
	d := 3 + r.n(4)
	for i := range g.Lead {
		if i == 0 || r.f() < density {
			d = max(1, min(d+r.n(5)-2, Degrees))
			g.Lead[i] = d
		}
	}
	for i := 0; i < Steps; i += 4 {
		g.Bass[i] = []int{1, 1, 5, 1}[i/4]
		if r.f() < 0.4 {
			g.Bass[i+2] = []int{1, 5, 3}[r.n(3)]
		}
	}
	g.Pulse[0], g.Pulse[8] = true, true
	if r.f() < 0.5 {
		g.Pulse[12] = true
	}
	// The mood's groove, written out: its chance steps (busy steps and fills)
	// drawn once, after everything above, so drums never shift a seed's lead
	// or bass. Each level is written as the nearest symbol the grid has.
	if groove, ok := grooveLib[p.Groove]; ok {
		for d, lane := range groove {
			b := []byte(lane)
			for i := range b {
				if b[i] == '|' { // a polymetric lane stays one
					continue
				}
				switch v, _, flam := stepHit(b[i], p, &r); {
				case flam:
					b[i] = 'g'
				case v >= 0.85:
					b[i] = 'X'
				case v >= 0.55:
					b[i] = 'x'
				case v > 0:
					b[i] = 'o'
				default:
					b[i] = '.'
				}
			}
			g.Drums[d] = string(b)
		}
	}
	return g
}

// gridHit is a grid drum lane's hit at row, run rows into the grid's run. A
// grid plays the fixed steps only, X x o and g; anything else rests.
func gridHit(lane string, row, run int) drumHit {
	on, off := laneStep(lane, 2*row, 2*run), laneStep(lane, 2*row+1, 2*run+1)
	return drumHit{
		on: gridDrumGain * drumLevel(on), off: gridDrumGain * drumLevel(off),
		onFlam: on == 'g', offFlam: off == 'g',
	}
}

// RowAt reports which grid row was sounding at a given output frame, for a
// playhead that follows what is heard rather than what has been rendered
// (a browser output plays a few hundred ms behind the synth). It returns -1
// for a frame older than the last 64 rows.
func (s *Synth) RowAt(frame int64) int {
	n := s.rowLog.n.Load()
	for i := int64(0); i < min(n, int64(len(s.rowLog.at))); i++ {
		e := s.rowLog.at[(n-1-i)%int64(len(s.rowLog.at))].Load()
		if start, row := e>>8, int(e&0xff); start <= frame {
			return row
		}
	}
	return -1
}

// Frames is how many frames the synth has rendered.
func (s *Synth) Frames() int64 { return s.frames.Load() }

// rowLog remembers when recent rows started: each entry packs the start
// frame (<<8) with the row number.
type rowLog struct {
	at [64]atomic.Int64
	n  atomic.Int64
}

func (l *rowLog) add(frame int64, row int) {
	n := l.n.Load()
	l.at[n%int64(len(l.at))].Store(frame<<8 | int64(row))
	l.n.Store(n + 1)
}
