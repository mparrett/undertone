package engine

import (
	"slices"
	"sync/atomic"
)

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
// grid stays in key when the mood or root changes. Lead replaces the
// generator's lead and pulse its tension pulse; bass plays a track of its
// own, which generated music leaves silent. Drums
// replace the generator's drums, one lane each for kick, clap and shaker, in
// groove notation: 2*Steps sixteenths of X (accent), x (hit), o (ghost), g
// (flam) or . (rest), and the generator's chance steps, ? (a soft hit at
// DrumBusy) and f (a fill at the tension's chance), drawn afresh each time
// they come round; a short lane rests for the remainder, and a lane ending
// in | loops at its own length instead (polymetric). With Bed set, the
// generator keeps playing drone, fifth, pad and air underneath.
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
	trackFifth = 1
	trackLead  = 3
	trackPulse = 5
	// The bass once took over the fifth's track, which cost nothing while a
	// grid replaced the whole generator but dropped the fifth from the bed
	// under every theme once Bed kept it playing.
	trackBass = 6
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

// Rewind starts what is playing over within one block: the grid set with
// SetGrid or the generator from the first row of a new pattern, a theme from
// its first grid. The pattern that was playing is cut, as PlayTheme cuts one,
// and notes already sounding ring out. For a host's "from the top" button.
// Safe to call from any goroutine.
func (s *Synth) Rewind() { s.rewind.Store(true) }

// startOver runs on the audio thread at a block boundary, before the row
// step, so the first row steps on this frame.
func (s *Synth) startOver() {
	if s.theme != nil {
		// From the top, the theme plays as if just started: an EndTheme
		// counted from where it was would cut the replay short.
		s.themeAt, s.themeFresh, s.themeEnds = 0, true, false
	}
	if s.row != 0 {
		s.pat++ // the cut pattern ends here, as in startTheme
	}
	s.row, s.rowLeft, s.leadIn, s.cut = 0, 0, true, true
	// a grid's polymetric lanes count from here too; a theme's already do,
	// from its fresh first grid
	s.runFrom = s.pat * rows
}

// DegreeNote is the note grid degree d (1-based) plays above base in p's
// scale, for a host that labels a grid's rows; GridBases gives the bases.
func DegreeNote(p Params, base, d int) int { return degreeNote(p, base, d) }

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
	gridBassGain  = 3.4  // pluck two octaves down: a string bass. A palette's own bass plays at it too, so its :out sets its level against pluck's
	gridPulseGain = 1.1  //
	gridBedScale  = 0.75 // the generator's drone, fifth, pad and air, under a grid
	gridDrumGain  = 0.8  // a seeded village groove, about -22 dB: level with the bed (2026-10-12)
)

var gridBass = pluck

// gridBassVoiceGain is the bass lane's gain for each lead instrument
// ApplyBass can put there, measured to match the string's level (rms while
// sounding, bass lane alone, as of 2026-11-22): at the string's gain a bell is
// 15 dB hotter. TestApplyBassLevel holds them to it. One left out plays at
// gridBassGain.
var gridBassVoiceGain = map[string]float64{
	"bell": 0.52, "chime": 0.59, "clang": 1.14, "kalimba": 1.28, "reed": 1.08, "pluck": gridBassGain,
}

// applyGrid overwrites this row of the generated pattern with the grid's.
func (s *Synth) applyGrid(g *Grid, row int) {
	p := s.params
	lead, bass := GridBases(p)
	for _, t := range []int{0, trackFifth, 2, 4} {
		if g.Bed {
			s.pats[t][row].gain *= gridBedScale
			continue
		}
		s.pats[t][row] = trackRow{}
		// a held drone or pad from before the bed went off, at whatever row
		// it did: SetGrid promises the next row, not the next pattern
		s.voices[t].gate = false
	}
	for d, lane := range g.Drums {
		s.drums[d][row] = gridHit(lane, d, row, s.pat*rows+row-s.runFrom, &p)
	}
	s.pats[trackLead][row] = trackRow{}
	if d := g.Lead[row]; d > 0 {
		gain, ok := gridLeadGain[p.LeadName]
		if !ok {
			gain = 0.55 // a voice added without measuring it
		}
		s.pats[trackLead][row] = trackRow{degreeNote(p, lead, d), gain * trimGain(p.LeadDB, p.MoodLeadDB), p.LeadGate, p.Palette.Lead}
	}
	s.pats[trackBass][row] = trackRow{}
	if d := g.Bass[row]; d > 0 {
		gain := p.bassGain
		if gain == 0 {
			gain = gridBassGain
		}
		s.pats[trackBass][row] = trackRow{degreeNote(p, bass, d), gain * dbGain(p.BassDB), 2, p.Palette.bass()}
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
	density := max(0.35, min(p.LeadDensity*2, 0.7))
	d := 3 + r.n(4)
	for i := range g.Lead {
		if i == 0 || r.f() < density {
			d = max(1, min(d+r.n(5)-2, Degrees))
			g.Lead[i] = d
		}
	}
	// Degrees 5 and 3 are the fifth and third only in seven tones, so find
	// them by pitch; a scale without one (locrian has no perfect fifth)
	// keeps the seven-tone degree.
	fifth := toneDegree(p.Scale, 5, 7)
	third := toneDegree(p.Scale, 3, 3, 4)
	for i := 0; i < Steps; i += 4 {
		g.Bass[i] = []int{1, 1, fifth, 1}[i/4]
		if r.f() < 0.4 {
			g.Bass[i+2] = []int{1, fifth, third}[r.n(3)]
		}
	}
	g.Pulse[0], g.Pulse[8] = true, true
	if r.f() < 0.5 {
		g.Pulse[12] = true
	}
	// The mood's groove, written out. Its chance steps, ? and f, stay chance,
	// as a grid plays them; the rest (ratchets) are drawn once, after
	// everything above, so drums never shift a seed's lead or bass, each
	// level written as the nearest symbol the grid has.
	if groove, ok := grooveLib[p.Groove]; ok {
		for d, lane := range groove {
			b := []byte(lane)
			for i := range b {
				if b[i] == '|' || b[i] == '?' || b[i] == 'f' { // a polymetric lane stays one; chance stays chance
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

// toneDegree is the degree (1-based) of scale's first tone among semitones,
// or fallback if it has none of them.
func toneDegree(scale []int, fallback int, semitones ...int) int {
	for i, t := range scale {
		if slices.Contains(semitones, t) {
			return i + 1
		}
	}
	return fallback
}

// gridHit is drum lane d's hit at row, run rows into the grid's run. A grid
// plays its fixed steps, X x o and g, as written, and its chance steps, ? and
// f, as the generator does, by p's busyness and tension; anything else rests.
func gridHit(lane string, d, row, run int, p *Params) drumHit {
	var h drumHit
	for half := range 2 {
		c := laneStep(lane, 2*row+half, 2*run+half)
		v, flam := drumLevel(c), c == 'g'
		if c == '?' || c == 'f' {
			v, _, flam = stepHit(c, *p, chanceRng(p.Seed, d, 2*run+half))
		}
		if half == 0 {
			h.on, h.onFlam = gridDrumGain*v, flam
		} else {
			h.off, h.offFlam = gridDrumGain*v, flam
		}
	}
	return h
}

// chanceRng draws for a grid's chance step: one stream per seed, lane and
// sixteenth of the run, so a step is drawn afresh each time it comes round,
// and drawn the same again for the same seed, as the generator's are. The
// multipliers spread the three apart before xorshift sees them; its first
// output from nearby states would still be alike, so one is discarded.
func chanceRng(seed uint64, lane, sixteenth int) *rng {
	r := &rng{(seed*0x9e3779b97f4a7c15 ^ uint64(sixteenth)*0xbf58476d1ce4e5b9 ^ uint64(lane+1)*0x94d049bb133111eb) + grooveSalt | 1}
	r.next()
	return r
}

// RowAt reports which grid row was sounding at a given output frame, for a
// playhead that follows what is heard rather than what has been rendered
// (a browser output plays a few hundred ms behind the synth). It returns -1
// for a frame older than the last 64 rows.
func (s *Synth) RowAt(frame int64) int {
	if p, ok := s.PositionAt(frame); ok {
		return p.Row
	}
	return -1
}

// Frames is how many frames the synth has rendered.
func (s *Synth) Frames() int64 { return s.frames.Load() }

// rowLog remembers when recent rows started: each entry packs the start
// frame (<<8) with the row number, and pat holds the entry's pattern. It has
// a slot more than the 64 rows it promises, since find gives up the oldest
// slot as possibly mid-overwrite.
type rowLog struct {
	at  [65]atomic.Int64
	pat [65]atomic.Int64
	n   atomic.Int64
}

func (l *rowLog) add(frame int64, row, pat int) {
	n := l.n.Load()
	i := n % int64(len(l.at))
	l.at[i].Store(frame<<8 | int64(row))
	l.pat[i].Store(int64(pat))
	l.n.Store(n + 1)
}

// find returns the newest entry that started at or before frame. An entry's
// two words are written one after the other, so a reader the writer laps
// meanwhile could pair one row's frame with a later row's pattern. It reads
// the count again after each entry, and treats an entry the writer may have
// begun to overwrite (a whole log behind the next to be written) as gone.
func (l *rowLog) find(frame int64) (start int64, row, pat int, ok bool) {
	size := int64(len(l.at))
	n := l.n.Load()
	for k := n - 1; k >= max(0, n-size); k-- {
		e, pt := l.at[k%size].Load(), l.pat[k%size].Load()
		if k <= l.n.Load()-size {
			return 0, 0, 0, false
		}
		if start := e >> 8; start <= frame {
			return start, int(e & 0xff), int(pt), true
		}
	}
	return 0, 0, 0, false
}
