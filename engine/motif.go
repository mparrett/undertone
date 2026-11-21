package engine

import (
	"math"
	"strings"
)

// Phrasing and motif memory: a spike, each off unless Params.Phrasing or
// Params.Memory is set, or the host opts into the mood's own with
// Params.UsePhrasing or UseMemory.
//
// Without them, every pattern is drawn fresh from the seed and the params, so
// over minutes only tension moves the music: one phrase that never ends,
// held, which is the identity phrasing. With memory:
//
//   - a phrase holds a cycle of 2-4 chords, taken from the generator's own
//     chord draws, and the lead and pad keep one line per chord of the
//     cycle, so a repeated line comes back over the chord it was first
//     played on;
//   - at each boundary the line for this chord repeats, changes one thing,
//     or is drawn anew.
//
// With phrasing:
//
//   - a slow arc over 8-32 patterns (build, hold, thin, break, return) takes
//     layers out and brings them back, gives each section its own colour
//     (filters, grit, room, echo, the lead's register, the drums' busyness),
//     and, with memory too, on return restores the phrase's home chords and
//     lines;
//   - tension shortens phrases, holds off a break, and keeps the drums in; with
//     memory it also makes the lines more restless.
//
// Each runs alone. Memory under the identity phrasing develops its lines in
// one endless hold, with no return to restore them; phrasing without memory
// arranges and colours the generator's own chords and lines.
//
// Every choice comes from an rng keyed by the seed and the pattern or phrase,
// so a seed still determines the music. All the state is fixed-size and lives
// in the Synth: the boundary and the mix stage run on the audio thread and
// allocate nothing.

// Temperament is how a mood behaves over minutes: its phrasing and its
// memory, for a host to opt into either or both.
type Temperament struct {
	Phrasing Phrasing
	Memory   Memory
}

// Phrasing is the arc a mood's phrases take: how long they run, how often
// they go quiet, which layers leave first when they thin, and how each
// section sounds. The zero value is the identity phrasing: one phrase that
// never ends, held, with no layer out and no colour, which plays as if
// Params.Phrasing were nil.
type Phrasing struct {
	// PhraseMin and PhraseMax bound a phrase's length in patterns. PhraseMax
	// 0 is the identity: one endless hold.
	PhraseMin, PhraseMax int
	// BreakChance is the chance a phrase drops to its anchor layers for a
	// pattern or two before it returns.
	BreakChance float64
	// Drop lists the layers the arc may take out, first to go first and last
	// to come in. Layers not listed are the anchor: they always play.
	Drop []Layers
	// Shade is each section's colour, in section order: build, hold, thin,
	// break, return.
	Shade [nSections]Shade
}

// Memory is how a mood's lines develop: each chord of a cycle keeps a lead
// and a pad line, which at a boundary repeat, change one thing or are drawn
// anew. NewMemory gives the defaults for a restlessness; a zero field means
// what it says (a zero Leap never redraws), not "the default".
type Memory struct {
	// Restlessness, 0..1, is how often a line changes at a boundary; the rest
	// of the time it repeats. At 0 the first lines play forever.
	Restlessness float64
	// Leap, 0..1, is the share of changes that redraw the whole line; the
	// rest change one thing. Low evolves a theme, high keeps starting anew.
	Leap float64
	// ReturnDrift, 0..1, lets a return's lines change too, at that share of
	// a hold's rate. At 0 a return restates the home lines exactly.
	ReturnDrift float64
	// CycleChurn, 0..1, is the chance a phrase brings a new chord cycle,
	// raised by tension as Restlessness is; the lines carry over.
	CycleChurn float64
}

// NewMemory is a memory with the given restlessness and the defaults for
// the rest: a quarter of changes redraw, a return comes home exactly, and
// the chords move on at 0.6 of the restlessness.
func NewMemory(restlessness float64) Memory {
	return Memory{Restlessness: restlessness, Leap: 0.25, CycleChurn: 0.6 * restlessness}
}

// phrasing reports whether p's phrasing is other than the identity.
func (p *Params) phrasing() bool { return p.Phrasing != nil && p.Phrasing.PhraseMax > 0 }

// motifOn reports whether the motif code runs at all. When it doesn't, the
// generator plays exactly as it did before phrasing and memory existed.
func (p *Params) motifOn() bool { return p.phrasing() || p.Memory != nil }

// shadeOf is a section's colour under p's phrasing; the identity has none.
func shadeOf(p *Params, sec section) Shade {
	if !p.phrasing() {
		return Shade{}
	}
	return p.Phrasing.Shade[sec]
}

// Shade is a section's colour: offsets on the mood's derived params while the
// section plays. The continuous ones (Open, Grit, Dark, Wash, Room, Echo)
// glide into a section over its first shadeRows rows; the rest change at the
// section's first boundary.
type Shade struct {
	Open float64 // added to Params.Open: moves the mood's own svf filters
	Grit float64 // added to Params.Grit
	// Dark, 0..1, lowpasses the whole music mix: 0 is open, 1 about 300 Hz.
	// It closes moods whose patches have no svf for Open to move.
	Dark float64
	// Wash above 0 sends that share of the music mix to the reverb, so a mood
	// with no reverb sends of its own gets a room; below 0 it turns the
	// patches' own reverb sends down by that share, for a drier section.
	Wash float64
	Room float64 // added to Room.Size: how long the reverb rings
	Echo float64 // scales the delay sends by 1+Echo
	Lift int     // the lead's register, in octaves
	Busy float64 // added to DrumBusy
	Pad  int     // -1 silences the pad's second strike, +1 sounds both
	Fill bool    // the pattern before this section ends in a drum fill
}

// temperaments are chosen by reasoning, not yet by ear. The
// dungeon is patient: long phrases, a line that changes rarely, quiet that
// comes seldom; it has no filters of its own and no reverb, so its sections
// colour with Dark and Wash. Rust is restless: short phrases that break
// often, the machine dropping out and coming back with a fill. Neither
// drops the pulse: it carries tension, which the game owns.
var temperaments = map[string]Temperament{
	"dungeon": {Memory: NewMemory(0.3), Phrasing: Phrasing{PhraseMin: 10, PhraseMax: 16, BreakChance: 0.35,
		Drop: []Layers{LayerLead, LayerPad, LayerFifth, LayerAir},
		Shade: [nSections]Shade{
			secBuild:  {Dark: 0.35, Wash: 0.25}, // far off, coming into focus
			secHold:   {Dark: 0.1},
			secThin:   {Dark: 0.25, Wash: 0.3, Echo: 0.6, Pad: -1}, // sparser, wetter
			secBreak:  {Dark: 0.6, Wash: 0.5, Room: 0.3},           // closed, a long room
			secReturn: {Echo: -0.4, Pad: 1},                        // open, dry, full
		}}},
	"rust": {Memory: NewMemory(0.55), Phrasing: Phrasing{PhraseMin: 8, PhraseMax: 12, BreakChance: 0.7,
		Drop: []Layers{LayerPad, LayerLead, LayerDrums, LayerFifth},
		Shade: [nSections]Shade{
			secBuild:  {Open: -0.4, Wash: 0.15},
			secHold:   {},
			secThin:   {Open: -0.25, Wash: 0.2, Echo: 0.6, Lift: -1, Busy: -0.2, Pad: -1},
			secBreak:  {Open: -0.6, Grit: 0.2, Wash: 0.35, Room: 0.4}, // the machine idling in a hall
			secReturn: {Open: 0.35, Grit: 0.1, Wash: -0.4, Echo: -0.4, Busy: 0.3, Pad: 1, Fill: true},
		}}},
	// The other moods' are first guesses by character, for hearing the arc
	// across the range; not tuned by ear. Their colour is bolder than the
	// dungeon's, whose measured under 1 dB against no colour at all. Each
	// moves both Open (for patches with svf filters) and Dark (for those
	// without), since which a mood's patches have isn't checked here.
	"aurora": {Memory: NewMemory(0.25), Phrasing: Phrasing{PhraseMin: 10, PhraseMax: 16, BreakChance: 0.3, // slow, shimmering
		Drop: []Layers{LayerLead, LayerFifth, LayerPad},
		Shade: [nSections]Shade{
			secBuild:  {Dark: 0.3, Wash: 0.35},
			secThin:   {Wash: 0.45, Echo: 0.8, Room: 0.3, Pad: -1},
			secBreak:  {Open: -0.4, Dark: 0.55, Wash: 0.6, Room: 0.5},
			secReturn: {Open: 0.3, Wash: -0.2, Echo: -0.3, Lift: 1, Pad: 1},
		}}},
	"meadow": {Memory: NewMemory(0.4), Phrasing: Phrasing{PhraseMin: 8, PhraseMax: 12, BreakChance: 0.4, // light, open air
		Drop: []Layers{LayerLead, LayerPad, LayerDrums, LayerFifth},
		Shade: [nSections]Shade{
			secBuild:  {Open: -0.3, Dark: 0.25},
			secThin:   {Wash: 0.3, Echo: 0.6, Busy: -0.3, Pad: -1},
			secBreak:  {Open: -0.45, Dark: 0.45, Wash: 0.35},
			secReturn: {Open: 0.35, Wash: -0.2, Busy: 0.25, Pad: 1, Fill: true},
		}}},
	"forge": {Memory: NewMemory(0.5), Phrasing: Phrasing{PhraseMin: 8, PhraseMax: 12, BreakChance: 0.6, // heavy, hammering
		Drop: []Layers{LayerPad, LayerLead, LayerFifth, LayerDrums},
		Shade: [nSections]Shade{
			secBuild:  {Open: -0.4, Dark: 0.3},
			secThin:   {Open: -0.2, Echo: 0.5, Busy: -0.3, Lift: -1, Pad: -1},
			secBreak:  {Open: -0.6, Grit: 0.3, Dark: 0.35, Room: 0.3},
			secReturn: {Open: 0.35, Grit: 0.15, Wash: -0.3, Busy: 0.35, Pad: 1, Fill: true},
		}}},
	"void": {Memory: NewMemory(0.2), Phrasing: Phrasing{PhraseMin: 12, PhraseMax: 16, BreakChance: 0.5, // sparse, patient
		Drop: []Layers{LayerLead, LayerFifth, LayerPad},
		Shade: [nSections]Shade{
			secBuild:  {Dark: 0.45, Wash: 0.4},
			secThin:   {Dark: 0.3, Wash: 0.5, Echo: 0.8, Pad: -1},
			secBreak:  {Open: -0.5, Dark: 0.7, Wash: 0.6, Room: 0.5},
			secReturn: {Open: 0.2, Wash: -0.3, Echo: -0.3, Pad: 1},
		}}},
	"village": {Memory: NewMemory(0.5), Phrasing: Phrasing{PhraseMin: 8, PhraseMax: 12, BreakChance: 0.45, // lively, busy
		Drop: []Layers{LayerPad, LayerLead, LayerDrums, LayerFifth},
		Shade: [nSections]Shade{
			secBuild:  {Open: -0.3, Dark: 0.2, Busy: -0.2},
			secThin:   {Wash: 0.25, Echo: 0.5, Busy: -0.35, Pad: -1},
			secBreak:  {Open: -0.45, Dark: 0.4, Wash: 0.3},
			secReturn: {Open: 0.35, Wash: -0.3, Busy: 0.35, Pad: 1, Fill: true},
		}}},
	"stratosphere": {Memory: NewMemory(0.35), Phrasing: Phrasing{PhraseMin: 10, PhraseMax: 14, BreakChance: 0.4, // high, airy
		Drop: []Layers{LayerLead, LayerPad, LayerFifth},
		Shade: [nSections]Shade{
			secBuild:  {Dark: 0.3, Wash: 0.4},
			secThin:   {Wash: 0.45, Echo: 0.7, Lift: 1, Pad: -1},
			secBreak:  {Open: -0.4, Dark: 0.45, Wash: 0.6, Room: 0.5},
			secReturn: {Open: 0.3, Wash: -0.2, Echo: -0.3, Pad: 1},
		}}},
	"overworld": {Memory: NewMemory(0.45), Phrasing: Phrasing{PhraseMin: 8, PhraseMax: 12, BreakChance: 0.5, // open, travelling
		Drop: []Layers{LayerPad, LayerLead, LayerFifth, LayerDrums},
		Shade: [nSections]Shade{
			secBuild:  {Open: -0.3, Dark: 0.3},
			secThin:   {Wash: 0.3, Echo: 0.6, Busy: -0.25, Pad: -1},
			secBreak:  {Open: -0.5, Dark: 0.5, Wash: 0.4, Room: 0.3},
			secReturn: {Open: 0.35, Wash: -0.3, Busy: 0.3, Pad: 1, Fill: true},
		}}},
}

// builtinPhrasing and builtinMemory give a built-in mood row its
// temperament. They are called from the moods table's literal, not set in an
// init, so a package-level derive (initialized before any init runs) still
// gets them.
func builtinPhrasing(mood string) *Phrasing {
	t, ok := temperaments[mood]
	if !ok {
		return nil
	}
	return &t.Phrasing
}

func builtinMemory(mood string) *Memory {
	t, ok := temperaments[mood]
	if !ok {
		return nil
	}
	return &t.Memory
}

// TemperamentFor is a built-in mood's temperament. A derive already hands
// it on as Params.MoodPhrasing and MoodMemory, for a host to opt into with
// UsePhrasing and UseMemory; this is for setting Phrasing or Memory
// directly, say to vary one. Moods without one report false.
func TemperamentFor(mood string) (Temperament, bool) {
	t, ok := temperaments[mood]
	return t, ok
}

type section uint8

const (
	secBuild section = iota
	secHold
	secThin
	secBreak
	secReturn
	nSections
)

var sectionNames = [nSections]string{"build", "hold", "thin", "break", "return"}

func (s section) String() string { return sectionNames[s] }

// SectionNames are the phrase's sections in Phrasing.Shade's order.
func SectionNames() []string { return sectionNames[:] }

// change scales the restlessness in each section: a build states its line, a
// hold lets it drift, a thin frays it and a break holds still. A return's
// is the mood's ReturnDrift: by default it comes home unchanged.
var change = [nSections]float64{0.3, 1, 1.6, 0, 0}

// Tension coupling. Tension is read from Params.PulseGain, which a mood
// derives as the tension itself.
//   - A phrase is planned shorter as tension rises: by up to 40% at 1, and
//     never under 6 patterns, so the hold still outlasts the return.
//   - Restlessness rises with it: R × (1 + tension), at most 1.
//   - No break while tension is rising (it rose since the last boundary) or
//     at breakTension and up: the phrase goes from thin straight to return.
//   - The drums are never taken out or halved at drumsStay and up.
const (
	breakTension = 0.6
	drumsStay    = 0.5
	risingBy     = 0.004
)

// maxProg is the longest chord cycle. A chord is an index into the chords
// the generator draws: tonic (drawn twice as often), VI, iv.
const maxProg = 4

var chordNames = [4]string{"i", "i", "VI", "iv"}

// shadeRows is how many rows a section's colour takes to glide in: about
// 3-4 s at these tempos.
const shadeRows = 8

// leadLine is the lead's line in scale degrees from the chord, so a repeat
// follows the chord the pattern draws.
type leadLine struct {
	on  [rows]bool
	deg [rows]int8
}

// padLine is the pad's two strikes, at rows 0 and 8, as an index into the
// stacked tones 0, 2, 4.
type padLine struct {
	on    [2]bool
	voice [2]int8
}

type motifState struct {
	started  bool
	phr, mem bool // phrasing other than the identity; memory

	// One line per slot of the chord cycle; have marks the slots played.
	leads [maxProg]leadLine
	pads  [maxProg]padLine
	have  [maxProg]bool
	prog  [maxProg]int8 // the cycle's chords
	nProg int
	// home is the phrase's theme, chords and lines, which a return restores
	homeLeads [maxProg]leadLine
	homePads  [maxProg]padLine
	homeHave  [maxProg]bool
	homeProg  [maxProg]int8
	homeNProg int

	cyc   int // patterns since the cycle last started
	slot  int // the cycle slot this pattern plays
	chord int // its chord
	fresh bool
	// what this slot's lines were before this pattern changed them, so a
	// mid-pattern SetParametersNow reshapes the same pattern from the same start
	prevLead leadLine
	prevPad  padLine

	sec    section
	secPos int // patterns into the section
	lens   [nSections]int
	phrase int

	tension, prevTension float64
	fill                 bool // this pattern ends in a fill into the next section
	held                 bool // tension held off this phrase's break

	sp       Params // the params shaded for this pattern
	from, to Shade  // the colour gliding out and the one gliding in
	glideIn  bool   // this pattern is the section's first: glide over its rows
	cur      Shade  // the colour last pushed
	mix      mixShade

	// what the last boundary did to each lane, for MotifTrace
	lastLead, lastPad string
}

// motifRow runs at every row with phrasing or memory set: at a pattern
// boundary it moves the arc on and builds the pattern; at every row, with
// phrasing, it pushes the section's colour to the voices.
func (s *Synth) motifRow() {
	if s.row == 0 {
		s.motif.advance(&s.params, s.pat)
		s.motifBuild(s.pat)
	}
	if s.motif.phr {
		s.pushShade(s.row)
	}
}

// motifNow redoes the pattern for a SetParametersNow mid-pattern.
func (s *Synth) motifNow(pat, row int) {
	s.motifBuild(pat)
	if s.motif.phr {
		s.pushShade(row)
	}
}

// motifBuild builds the pattern: on the cycle's chord with memory, the
// generator's own without; with the section's colour only under phrasing,
// since the identity's must not run at all to stay bit-exact.
func (s *Synth) motifBuild(pat int) {
	m := &s.motif
	m.sp = s.params
	if m.phr {
		m.sp = m.shaded(s.params)
	}
	chord := -1
	if m.mem {
		chord = m.chord
	}
	s.pats = buildPatternsOn(m.sp, pat, chord)
	s.drums = buildDrums(m.sp, pat)
	m.reshape(&m.sp, pat, &s.pats, &s.drums)
}

// pushShade sets the voices, the reverb and the mix stage to the section's
// colour as of row, gliding from the last section's over shadeRows rows.
func (s *Synth) pushShade(row int) {
	m := &s.motif
	b := 1.0
	if m.glideIn {
		b = min(1, float64(row+1)/shadeRows)
	}
	lerp := func(a, c float64) float64 { return a + (c-a)*b }
	c := Shade{
		Open: lerp(m.from.Open, m.to.Open),
		Grit: lerp(m.from.Grit, m.to.Grit),
		Dark: lerp(m.from.Dark, m.to.Dark),
		Wash: lerp(m.from.Wash, m.to.Wash),
		Room: lerp(m.from.Room, m.to.Room),
		Echo: lerp(m.from.Echo, m.to.Echo),
	}
	m.cur = c
	p := &s.params
	grit := clamp(p.Grit+c.Grit, 0, 1)
	open := clamp(p.Open+c.Open, -1, 1)
	// the same targets apply() sets, shaded
	for _, vs := range [][]Voice{s.voices, s.dvoices[:]} {
		for i := range vs {
			vs[i].gritTo = grit
			if !vs[i].active {
				vs[i].grit = grit
			}
		}
	}
	for i := range s.voices {
		s.voices[i].openTo = open
		if !s.voices[i].active {
			s.voices[i].open = open
		}
	}
	room := p.Room
	room.Size = clamp(room.Size+c.Room, -1, 1)
	s.verb.setRoom(room)
	m.mix.darkTo, m.mix.washTo, m.mix.echoTo = clamp(c.Dark, 0, 1), clamp(c.Wash, -1, 1), max(-1, c.Echo)
}

func clamp(x, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, x)) }

// mixShade is the mix stage the section colours: a lowpass over the music
// (Dark), a send of it to the reverb or a cut of the patches' own reverb
// sends (Wash), and a scale on the delay sends (Echo). It runs only with
// phrasing, so a render without is untouched.
type mixShade struct {
	dark, wash, echo       float64
	darkTo, washTo, echoTo float64
	a                      float64 // the lowpass coefficient for dark
	lpL, lpR               float64
	tick                   int
}

// mixStep is the share of the way to a target covered every 64 frames: a
// time constant of about 0.15 s, smoothing the per-row steps pushShade makes.
const mixStep = 1.0 / 100

func (x *mixShade) run(l, r *float64, b *sends) {
	if x.tick&63 == 0 {
		g := func(v *float64, to float64) {
			*v += (to - *v) * mixStep
			if math.Abs(to-*v) < 1e-4 {
				*v = to
			}
		}
		g(&x.dark, x.darkTo)
		g(&x.wash, x.washTo)
		g(&x.echo, x.echoTo)
		if x.dark == 0 {
			x.a = 1
		} else {
			fc := 20000 * math.Exp2(-6*x.dark)
			x.a = 1 - math.Exp(-2*math.Pi*fc/SampleRate)
		}
	}
	x.tick++
	x.lpL += (*l - x.lpL) * x.a
	x.lpR += (*r - x.lpR) * x.a
	*l, *r = x.lpL, x.lpR
	if x.wash > 0 {
		b.verb += (*l + *r) * 0.5 * x.wash
	} else {
		b.verb *= 1 + x.wash
	}
	b.delay *= 1 + x.echo
}

// advance moves the arc and the chord cycle on a pattern, at a boundary.
func (m *motifState) advance(p *Params, pat int) {
	T := clamp(p.PulseGain, 0, 1)
	secWas := m.sec
	first := !m.started || m.phr != p.phrasing() || m.mem != (p.Memory != nil)
	if first {
		// a host turning phrasing or memory on or off starts the arc afresh
		*m = motifState{started: true, phr: p.phrasing(), mem: p.Memory != nil, tension: T, prevTension: T}
		m.plan(p, pat, true)
		m.from, m.to = shadeOf(p, m.sec), shadeOf(p, m.sec)
	} else {
		m.prevTension, m.tension = m.tension, T
		m.secPos++
		m.cyc++
		for m.secPos >= m.lens[m.sec] {
			m.secPos = 0
			switch m.sec {
			case secBuild:
				m.saveHome()
			case secReturn:
				m.phrase++
				r := m.rng(p.Seed, uint64(m.phrase), 0x7a11)
				// a mood may let its theme wander between phrases: the lines
				// stay, under a new cycle of chords
				m.plan(p, pat, r.f() < m.churn(p))
				continue
			}
			m.sec++
			if m.sec == secBreak && m.lens[secBreak] > 0 {
				m.held = m.tension-m.prevTension > risingBy || m.tension >= breakTension
				if m.held {
					m.lens[secBreak] = 0
				}
			}
			if m.sec == secReturn {
				m.restoreHome()
			}
		}
	}
	if m.sec == secBreak {
		m.slot = 0 // a pedal: the cycle's first chord held through the break
	} else {
		m.slot = m.cyc % m.nProg
	}
	// the cycle drifts in a hold or thin: at its start, one chord may change
	if (m.sec == secHold || m.sec == secThin) && m.slot == 0 && m.cyc > 0 && m.nProg > 1 {
		r := m.rng(p.Seed, uint64(pat), 0xc40d)
		if r.f() < m.restless(p)*change[m.sec]*0.4 {
			k := 1 + r.n(m.nProg-1)
			was := m.prog[k]
			m.prog[k] = int8(chordDraw(*p, pat+k))
			// keep it a cycle: a drift to one chord throughout is undone
			same := true
			for i := range m.nProg {
				same = same && tonicOf(m.prog[i]) == tonicOf(m.prog[0])
			}
			if same {
				m.prog[k] = was
			}
		}
	}
	m.chord = int(m.prog[m.slot])
	if !m.mem {
		m.chord = chordDraw(*p, pat) // the generator's own
	}
	m.fresh = !m.have[m.slot]
	m.prevLead, m.prevPad = m.leads[m.slot], m.pads[m.slot]

	if !first {
		m.glideIn = m.sec != secWas || m.secPos == 0
		if m.glideIn {
			m.from, m.to = m.cur, shadeOf(p, m.sec)
		}
	}
	// a fill into the next section, on the last pattern of this one
	next := m.nextSection()
	if next == secBreak && (m.tension-m.prevTension > risingBy || m.tension >= breakTension) {
		next = secReturn // the break will be held off
	}
	m.fill = m.secPos == m.lens[m.sec]-1 && shadeOf(p, next).Fill
}

// nextSection is the section after this one, skipping those planned empty.
func (m *motifState) nextSection() section {
	for s := m.sec + 1; s < nSections; s++ {
		if m.lens[s] > 0 {
			return s
		}
	}
	return secHold
}

// restless is how often a line changes now; without memory nothing does.
func (m *motifState) restless(p *Params) float64 {
	if p.Memory == nil {
		return 0
	}
	return min(1, p.Memory.Restlessness*(1+m.tension))
}

// churn is the chance the next phrase brings a new chord cycle; without
// memory the cycle is never played, so it never changes.
func (m *motifState) churn(p *Params) float64 {
	if p.Memory == nil {
		return 0
	}
	// capped after the knob, where the old 0.6 × restless() capped before
	// it: the two differ only when Restlessness × (1+tension) passes 1
	return min(1, p.Memory.CycleChurn*(1+m.tension))
}

// leap and returnDrift are p's knobs, 0 without memory, so no caller
// depends on having checked p.Memory itself.
func leap(p *Params) float64 {
	if p.Memory == nil {
		return 0
	}
	return p.Memory.Leap
}

func returnDrift(p *Params) float64 {
	if p.Memory == nil {
		return 0
	}
	return p.Memory.ReturnDrift
}

func (m *motifState) saveHome() {
	m.homeLeads, m.homePads, m.homeHave = m.leads, m.pads, m.have
	m.homeProg, m.homeNProg = m.prog, m.nProg
}

func (m *motifState) restoreHome() {
	m.leads, m.pads, m.have = m.homeLeads, m.homePads, m.homeHave
	m.prog, m.nProg = m.homeProg, m.homeNProg
	m.cyc = 0
}

// shaded is p with the section's colour: the lead's register and the drums'
// busyness change what the pattern plays; the rest reaches the voices
// through pushShade and is set here to match.
func (m *motifState) shaded(p Params) Params {
	sh := m.to
	p.LeadLift += 12 * sh.Lift
	p.DrumBusy = clamp(p.DrumBusy+sh.Busy, 0, 1)
	p.Open = clamp(p.Open+sh.Open, -1, 1)
	p.Grit = clamp(p.Grit+sh.Grit, 0, 1)
	p.Room.Size = clamp(p.Room.Size+sh.Room, -1, 1)
	return p
}

// reshape decides this pattern's lines from prevLead and prevPad and writes
// them, the arc's layer bias and the section's colour into the pattern. It
// only reads the arc, so a mid-pattern call (applyNow) draws the same.
func (m *motifState) reshape(p *Params, pat int, pats *[nTracks][rows]trackRow, drums *[nDrums][rows]drumHit) {
	// m.mem is what advance last saw; a mid-pattern SetParametersNow can
	// take memory away before the next boundary resyncs it, and then this
	// pattern's rest is the generator's
	if !m.mem || p.Memory == nil {
		// the generator's lines as drawn, under the section's pad colour
		m.lastLead, m.lastPad = "gen", "gen"
		padShade(p, m.chord, m.to, pats)
		m.applyArc(p, pats, drums)
		if m.fill {
			fill(p, drums)
		}
		return
	}
	r := m.rng(p.Seed, uint64(pat), 0x5eed)
	mult := change[m.sec]
	if m.sec == secReturn {
		mult = returnDrift(p)
	}
	rate := m.restless(p) * mult
	k := m.slot
	m.leads[k], m.pads[k] = m.prevLead, m.prevPad
	switch {
	case m.fresh:
		m.leads[k], m.pads[k] = freshLead(p, m.chord, pats), freshPad(p, m.chord, pats)
		m.have[k] = true
		m.lastLead, m.lastPad = "fresh", "fresh"
		// a slot's first statement is its home line, so a return brings back
		// what was heard there and not a new draw
		if !m.homeHave[k] {
			m.homeLeads[k], m.homePads[k], m.homeHave[k] = m.leads[k], m.pads[k], true
		}
	case m.sec == secReturn && rate == 0:
		m.lastLead, m.lastPad = "home", "home"
	default:
		m.lastLead = m.decideLead(p, m.chord, pats, &r, rate)
		m.lastPad = m.decidePad(p, m.chord, pats, &r, rate)
	}
	chord := chordAt(*p, m.chord)
	lead, pad := m.leads[k], m.pads[k]
	if p.LeadName != "" {
		g := 0.55 * leadGain(p.LeadName) * trimGain(p.LeadDB, p.MoodLeadDB) // as buildPatterns writes it
		for i := range rows {
			pats[3][i] = trackRow{}
			if lead.on[i] {
				pats[3][i] = trackRow{chord + scaleTone(p.Scale, int(lead.deg[i])) + p.LeadLift, g, p.LeadGate, p.Palette.Lead}
			}
		}
	}
	// the section's pad colour is the pattern's, not the line's to keep
	switch sh := m.to; {
	case sh.Pad < 0:
		pad.on[1] = false
	case sh.Pad > 0 && !pad.on[1]:
		pad.on[1], pad.voice[1] = true, (pad.voice[0]+1)%3
	}
	for k, row := range []int{0, 8} {
		pats[2][row] = trackRow{}
		if pad.on[k] {
			pats[2][row] = trackRow{chord + p.Scale[2*pad.voice[k]], 0.7 * trimGain(p.PadDB, p.MoodPadDB), 6, p.Palette.Pad}
		}
	}
	m.applyArc(p, pats, drums)
	if m.fill {
		fill(p, drums)
	}
}

// padShade is the section's pad colour on the generator's own pad: the second
// strike out, or sounded a stacked tone above the first.
func padShade(p *Params, chordIdx int, sh Shade, pats *[nTracks][rows]trackRow) {
	switch {
	case sh.Pad < 0:
		pats[2][8] = trackRow{}
	case sh.Pad > 0 && pats[2][8].note == 0 && pats[2][0].note != 0:
		if l := freshPad(p, chordIdx, pats); l.on[0] {
			tr := pats[2][0]
			tr.note = chordAt(*p, chordIdx) + p.Scale[2*((l.voice[0]+1)%3)]
			pats[2][8] = tr
		}
	}
}

// fill writes a drum fill over the pattern's last four rows: the backbeat
// lane rising and ratcheting, a kick under its start. It plays even where the
// arc has the drums out, so a break can end in one, and at a floor gain, so
// the drums a low tension barely lets in still mark it. A mood with no drums
// playing gets none.
func fill(p *Params, drums *[nDrums][rows]drumHit) {
	if p.Groove == "" || p.DrumGain <= 0 {
		return
	}
	g := max(p.DrumGain, 0.3)
	for i := rows - 4; i < rows; i++ {
		j := i - (rows - 4)
		drums[1][i] = drumHit{on: g * (0.5 + 0.12*float64(j)), onN: 1 + j/2}
		drums[2][i] = drumHit{}
	}
	drums[0][rows-4] = drumHit{on: g}
	drums[1][rows-1].off, drums[1][rows-1].offN = g*0.9, 3
}

// rng is a decision stream apart from the generator's, keyed by the seed, an
// index and a salt, and warmed past xorshift's weak first draws.
func (m *motifState) rng(seed, i, salt uint64) rng {
	r := rng{seed*0xd1342543de82ef95 ^ (i+salt)*0x2545f4914f6cdd1d | 1}
	r.next()
	r.next()
	return r
}

// plan draws the next phrase's section lengths, shorter as tension rises.
// Only the first phrase builds; later ones start from the hold a return leads
// into. The first phrase, or one whose theme wanders, draws a new chord cycle
// from the generator's draws for the patterns ahead; otherwise the phrase
// keeps the home cycle. The identity phrasing plans one endless hold, its
// cycle drawn once.
func (m *motifState) plan(p *Params, pat int, newCycle bool) {
	r := m.rng(p.Seed, uint64(m.phrase), 0xa4c)
	if !m.phr {
		m.drawCycle(&r, p, pat)
		m.lens = [nSections]int{secHold: math.MaxInt}
		m.sec, m.secPos, m.cyc = secHold, 0, 0
		return
	}
	t := p.Phrasing
	lo, hi := max(t.PhraseMin, 5), max(t.PhraseMax, t.PhraseMin, 5)
	n := lo + r.n(hi-lo+1)
	n = max(6, int(math.Round(float64(n)*(1-0.4*m.tension))))
	first := m.nProg == 0
	m.cyc = 0
	m.lens = [nSections]int{}
	if first {
		m.lens[secBuild] = max(2, n/4)
		n -= m.lens[secBuild]
	}
	if r.f() < t.BreakChance {
		m.lens[secBreak] = 1 + r.n(2)
	}
	// the cycle is drawn after the sections, so its draws leave theirs alone
	if newCycle || first {
		m.drawCycle(&r, p, pat)
	}
	m.lens[secThin] = max(1, n/5)
	// With memory, a return plays the whole home cycle at least once, and the
	// hold the whole cycle, so the return is a cycle already heard. Without
	// it the cycle is never played, so it mustn't size the sections.
	cycle := 0
	if m.mem {
		cycle = m.homeNProg
	}
	m.lens[secReturn] = max(2, n/5, cycle)
	m.lens[secHold] = max(2, cycle, n-m.lens[secThin]-m.lens[secReturn])
	m.sec, m.secPos = secHold, 0
	m.held = false
	if first {
		m.sec = secBuild
	}
}

// drawCycle draws a chord cycle from the generator's draws for the patterns
// ahead, and makes it home.
func (m *motifState) drawCycle(r *rng, p *Params, pat int) {
	m.nProg = 2 + r.n(maxProg-1)
	same := true
	for i := range m.nProg {
		m.prog[i] = int8(chordDraw(*p, pat+i))
		same = same && tonicOf(m.prog[i]) == tonicOf(m.prog[0])
	}
	// a cycle of one chord is no cycle: the last moves off the first
	if same {
		if tonicOf(m.prog[0]) == 0 {
			m.prog[m.nProg-1] = int8(2 + r.n(2))
		} else {
			m.prog[m.nProg-1] = 0
		}
	}
	m.homeProg, m.homeNProg = m.prog, m.nProg
}

// tonicOf folds the two tonic draws together.
func tonicOf(c int8) int8 {
	if c == 1 {
		return 0
	}
	return c
}

// applyArc takes layers out by the section: a build brings the dropped
// layers in one by one, last-listed first; a thin takes out the first listed
// and halves the next; a break leaves only the anchor. From drumsStay
// tension up the drums stay in whatever the section.
func (m *motifState) applyArc(p *Params, pats *[nTracks][rows]trackRow, drums *[nDrums][rows]drumHit) {
	// as in reshape: phrasing may have gone since the boundary
	if !m.phr || !p.phrasing() {
		return
	}
	t := p.Phrasing
	k := len(t.Drop)
	out, half := 0, 0
	switch m.sec {
	case secBuild:
		// the anchor alone at first, every layer in by the build's last pattern
		out = k - k*m.secPos/(m.lens[secBuild]-1)
	case secThin:
		out, half = min(1, k), min(2, k)
	case secBreak:
		out = k
	}
	for i, l := range t.Drop {
		if l == LayerDrums && m.tension >= drumsStay {
			continue
		}
		switch {
		case i < out:
			scaleLayer(l, 0, pats, drums)
		case i < half:
			scaleLayer(l, 0.5, pats, drums)
		}
	}
}

func scaleLayer(l Layers, g float64, pats *[nTracks][rows]trackRow, drums *[nDrums][rows]drumHit) {
	if l == LayerDrums {
		for d := range drums {
			for i := range rows {
				drums[d][i].on *= g
				drums[d][i].off *= g
			}
		}
		return
	}
	for tr := range nTracks {
		if l != Layers(1)<<tr {
			continue
		}
		for i := range rows {
			if g == 0 {
				pats[tr][i] = trackRow{}
			} else {
				pats[tr][i].gain *= g
			}
		}
	}
}

// decideLead repeats, mutates or redraws the slot's lead line, and says which.
func (m *motifState) decideLead(p *Params, chord int, pats *[nTracks][rows]trackRow, r *rng, rate float64) string {
	x := r.f()
	switch {
	case x < rate*leap(p):
		m.leads[m.slot] = freshLead(p, chord, pats)
		return "redraw"
	case x < rate:
		return m.leads[m.slot].mutate(p, r)
	}
	return "repeat"
}

func (m *motifState) decidePad(p *Params, chord int, pats *[nTracks][rows]trackRow, r *rng, rate float64) string {
	pad := &m.pads[m.slot]
	x := r.f()
	switch {
	case x < rate*leap(p):
		*pad = freshPad(p, chord, pats)
		return "redraw"
	case x < rate:
		// a strike in or out toward the pad's density, or a strike revoiced
		k := r.n(2)
		want := p.PadDensity * 2
		count := 0
		for _, on := range pad.on {
			if on {
				count++
			}
		}
		switch {
		case float64(count) < want-0.5 && !pad.on[k]:
			pad.on[k], pad.voice[k] = true, int8(r.n(3))
			return "add"
		case float64(count) > want+0.5 && pad.on[k]:
			pad.on[k] = false
			return "drop"
		case pad.on[k]:
			pad.voice[k] = int8((int(pad.voice[k]) + 1 + r.n(2)) % 3)
			return "revoice"
		}
	}
	return "repeat"
}

// mutate makes one bounded edit to the line: shift a note a degree or two,
// drop or add a note (toward the lead's density, so tension still thins it),
// rotate by a row or two, invert a step, or move a note an octave.
func (l *leadLine) mutate(p *Params, r *rng) string {
	n := len(p.Scale)
	lo, hi := 3, 2*n+3
	var idx [rows]int
	count := 0
	for i, on := range l.on {
		if on {
			idx[count] = i
			count++
		}
	}
	want := p.LeadDensity * rows
	if count == 0 {
		if want < 0.5 {
			return "repeat"
		}
		i := r.n(rows)
		l.on[i], l.deg[i] = true, int8(n+r.n(n))
		return "add"
	}
	pick := idx[r.n(count)]
	op := r.n(6)
	if float64(count) > want+1.5 {
		op = 1
	} else if float64(count) < want-1.5 {
		op = 2
	}
	clamp := func(d int) int8 { return int8(max(lo, min(d, hi))) }
	switch op {
	case 0:
		d := 1 + r.n(2)
		if r.n(2) == 0 {
			d = -d
		}
		l.deg[pick] = clamp(int(l.deg[pick]) + d)
		return "shift"
	case 1:
		if count > 1 {
			l.on[pick] = false
			return "drop"
		}
		return "repeat"
	case 2:
		if count == rows {
			return "repeat"
		}
		i := r.n(rows)
		for l.on[i] {
			i = (i + 1) % rows
		}
		// near the note before it, or the line's first note
		near := l.deg[idx[0]]
		for j := i - 1; j >= 0; j-- {
			if l.on[j] {
				near = l.deg[j]
				break
			}
		}
		l.on[i], l.deg[i] = true, clamp(int(near)+r.n(3)-1)
		return "add"
	case 3:
		k := 1 + r.n(2)
		if r.n(2) == 0 {
			k = rows - k
		}
		var on [rows]bool
		var deg [rows]int8
		for i := range rows {
			on[(i+k)%rows], deg[(i+k)%rows] = l.on[i], l.deg[i]
		}
		l.on, l.deg = on, deg
		return "rotate"
	case 4:
		// mirror a note about the one before it: a step up becomes a step down
		for j := pick - 1; j >= 0; j-- {
			if l.on[j] {
				l.deg[pick] = clamp(2*int(l.deg[j]) - int(l.deg[pick]))
				return "invert"
			}
		}
		fallthrough
	default:
		d := int(l.deg[pick])
		if d+n <= hi && (d-n < lo || r.n(2) == 0) {
			d += n
		} else if d-n >= lo {
			d -= n
		}
		l.deg[pick] = int8(d)
		return "octave"
	}
}

// chordDraw is the chord buildPatterns draws for a pattern: its first draw,
// repeated here. TestPatternChordMatches holds the two together.
func chordDraw(p Params, pat int) int {
	r := rng{p.Seed ^ uint64(pat)*0x9e3779b97f4a7c15 | 1}
	return r.n(4)
}

// chordAt is the note a chord stands for in p. VI and iv are the tones two
// and four degrees below the root, which a five-note scale also has.
func chordAt(p Params, k int) int {
	return p.Root + [4]int{0, 0, scaleTone(p.Scale, -2), scaleTone(p.Scale, -4)}[k]
}

// freshLead reads the generator's lead line from a pattern back into degrees.
func freshLead(p *Params, chordIdx int, pats *[nTracks][rows]trackRow) leadLine {
	var l leadLine
	if p.LeadName == "" {
		return l
	}
	chord := chordAt(*p, chordIdx)
	n := len(p.Scale)
	for i := range rows {
		tr := pats[3][i]
		if tr.note == 0 {
			continue
		}
		semis := tr.note - chord - p.LeadLift
		for d := 3; d <= 2*n+3; d++ {
			if scaleTone(p.Scale, d) == semis {
				l.on[i], l.deg[i] = true, int8(d)
				break
			}
		}
	}
	return l
}

func freshPad(p *Params, chordIdx int, pats *[nTracks][rows]trackRow) padLine {
	var l padLine
	chord := chordAt(*p, chordIdx)
	for k, row := range []int{0, 8} {
		tr := pats[2][row]
		if tr.note == 0 {
			continue
		}
		for v := range 3 {
			if chord+p.Scale[2*v] == tr.note {
				l.on[k], l.voice[k] = true, int8(v)
				break
			}
		}
	}
	return l
}

// MotifState is where the arc is, for a trace.
type MotifState struct {
	Section   string  // "hold" under the identity phrasing
	Phrase    int     // phrases since the start
	Lead, Pad string  // what the last boundary did to each line
	Chord     string  // this pattern's chord
	Cycle     string  // with memory, the phrase's chord cycle, this pattern's slot bracketed
	Tension   float64 // as the arc read it
	Fill      bool    // this pattern ends in a fill
	Held      bool    // tension held off this phrase's break
}

// MotifTrace reports where the arc is and what the last boundary did to the
// lead and pad lines. Under the identity phrasing, with or without memory, it
// is phrase 0 in a hold. It reads the audio thread's state unsynchronised and
// allocates: for an offline render that calls it between Reads, not for a
// live host.
func (s *Synth) MotifTrace() MotifState {
	m := &s.motif
	if !m.started {
		return MotifState{Section: secHold.String(), Lead: "gen", Pad: "gen"}
	}
	var b strings.Builder
	for i := range m.nProg * b2i(m.mem) {
		if i > 0 {
			b.WriteByte('-')
		}
		name := chordNames[m.prog[i]]
		if i == m.slot && m.sec != secBreak {
			name = "[" + name + "]"
		}
		b.WriteString(name)
	}
	return MotifState{
		Section: m.sec.String(), Phrase: m.phrase, Lead: m.lastLead, Pad: m.lastPad,
		Chord: chordNames[m.chord], Cycle: b.String(), Tension: m.tension,
		Fill: m.fill, Held: m.held,
	}
}
