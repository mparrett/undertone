package engine

// Motif memory and phrase arcs: a spike, off unless Params.Temperament is set.
//
// Without it, every pattern is drawn fresh from the seed and the params, so
// over minutes only tension moves the music. With it, the lead and pad keep
// the line they played last and, at each boundary, repeat it, change one
// thing in it, or draw a new one; and a slow arc over 8-32 patterns (build,
// hold, thin, break, return) takes layers out and brings them back, and sets
// how much the lines change. Every choice comes from an rng keyed by the seed
// and the pattern, so a seed still determines the music.
//
// All the state is fixed-size and lives in the Synth: the boundary runs on
// the audio thread and allocates nothing.

// Temperament is how a mood behaves over minutes: how restless its lines are,
// how long its phrases run, how often it goes quiet, and which layers leave
// first when it thins.
type Temperament struct {
	// Restlessness, 0..1, is how often a line changes at a boundary; the rest
	// of the time it repeats.
	Restlessness float64
	// PhraseMin and PhraseMax bound a phrase's length in patterns.
	PhraseMin, PhraseMax int
	// BreakChance is the chance a phrase drops to its anchor layers for a
	// pattern or two before it returns.
	BreakChance float64
	// Drop lists the layers the arc may take out, first to go first and last
	// to come in. Layers not listed are the anchor: they always play.
	Drop []Layers
}

// temperaments are the spike's two, chosen by ear. The dungeon is patient:
// long phrases, a line that changes rarely, and quiet that comes seldom. Rust
// is restless: short phrases that break often, the machine dropping out and
// slamming back. Neither drops the pulse: it carries tension, which the game
// owns.
var temperaments = map[string]Temperament{
	"dungeon": {Restlessness: 0.3, PhraseMin: 10, PhraseMax: 16, BreakChance: 0.35,
		Drop: []Layers{LayerLead, LayerPad, LayerFifth, LayerAir}},
	"rust": {Restlessness: 0.55, PhraseMin: 8, PhraseMax: 12, BreakChance: 0.7,
		Drop: []Layers{LayerPad, LayerLead, LayerDrums, LayerFifth}},
}

// TemperamentFor is a mood's temperament, for a host to opt in with
// p.Temperament = &t. Moods without one report false.
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

// change scales the temperament's restlessness in each section: a build
// states its line, a hold lets it drift, a thin frays it, a break holds still
// and a return comes home.
var change = [nSections]float64{0.3, 1, 1.6, 0, 0.5}

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
	started bool
	lead    leadLine
	pad     padLine
	// what the lines were before this pattern changed them, so a mid-pattern
	// SetParametersNow reshapes the same pattern from the same start
	prevLead leadLine
	prevPad  padLine
	// home is the phrase's theme, which a return brings back
	homeLead leadLine
	homePad  padLine

	sec    section
	secPos int // patterns into the section
	lens   [nSections]int
	phrase int
	// what the last boundary did to each lane, for MotifTrace
	lastLead, lastPad string
}

// boundary runs at a pattern boundary (Synth.step): it moves the arc on a
// pattern and reshapes the pattern just built.
func (m *motifState) boundary(p *Params, pat int, pats *[nTracks][rows]trackRow, drums *[nDrums][rows]drumHit) {
	t := p.Temperament
	if !m.started {
		m.started = true
		m.phrase = 0
		m.plan(t, p.Seed, true)
		m.lead = freshLead(p, pat, pats)
		m.pad = freshPad(p, pat, pats)
		m.homeLead, m.homePad = m.lead, m.pad
	} else {
		m.secPos++
		for m.secPos >= m.lens[m.sec] {
			m.secPos = 0
			if m.sec == secBuild {
				m.homeLead, m.homePad = m.lead, m.pad
			}
			if m.sec == secReturn {
				m.phrase++
				r := m.rng(p.Seed, uint64(m.phrase), 0x7a11)
				// a restless mood lets its theme wander between phrases
				if r.f() < t.Restlessness*0.6 {
					m.homeLead, m.homePad = m.lead, m.pad
				}
				m.plan(t, p.Seed, false)
				continue
			}
			m.sec++
		}
	}
	m.prevLead, m.prevPad = m.lead, m.pad
	m.reshape(p, pat, pats, drums)
}

// reshape decides this pattern's lines from prevLead and prevPad and writes
// them and the arc's layer bias into the pattern. It only reads the arc, so a
// mid-pattern call (applyNow) draws the same.
func (m *motifState) reshape(p *Params, pat int, pats *[nTracks][rows]trackRow, drums *[nDrums][rows]drumHit) {
	t := p.Temperament
	r := m.rng(p.Seed, uint64(pat), 0x5eed)
	rate := t.Restlessness * change[m.sec]
	m.lead, m.pad = m.prevLead, m.prevPad
	if m.sec == secReturn && m.secPos == 0 {
		m.lead, m.pad = m.homeLead, m.homePad
		m.lastLead, m.lastPad = "home", "home"
	} else {
		m.lastLead = m.decideLead(p, pat, pats, &r, rate)
		m.lastPad = m.decidePad(p, pat, pats, &r, rate)
	}
	chord := patternChord(*p, pat)
	if p.LeadName != "" {
		g := 0.55 * leadGain(p.LeadName)
		for i := range rows {
			pats[3][i] = trackRow{}
			if m.lead.on[i] {
				pats[3][i] = trackRow{chord + scaleTone(p.Scale, int(m.lead.deg[i])) + p.LeadLift, g, p.LeadGate, p.Palette.Lead}
			}
		}
	}
	for k, row := range []int{0, 8} {
		pats[2][row] = trackRow{}
		if m.pad.on[k] {
			pats[2][row] = trackRow{chord + p.Scale[2*m.pad.voice[k]], 0.7, 6, p.Palette.Pad}
		}
	}
	m.applyArc(t, pats, drums)
}

// rng is a decision stream apart from the generator's, keyed by the seed, an
// index and a salt, and warmed past xorshift's weak first draws.
func (m *motifState) rng(seed, i, salt uint64) rng {
	r := rng{seed*0xd1342543de82ef95 ^ (i+salt)*0x2545f4914f6cdd1d | 1}
	r.next()
	r.next()
	return r
}

// plan draws the next phrase's section lengths. Only the first phrase builds;
// later ones start from the hold a return leads into.
func (m *motifState) plan(t *Temperament, seed uint64, first bool) {
	r := m.rng(seed, uint64(m.phrase), 0xa4c)
	lo, hi := max(t.PhraseMin, 5), max(t.PhraseMax, t.PhraseMin, 5)
	n := lo + r.n(hi-lo+1)
	m.lens = [nSections]int{}
	if first {
		m.lens[secBuild] = max(2, n/4)
		n -= m.lens[secBuild]
	}
	if r.f() < t.BreakChance {
		m.lens[secBreak] = 1 + r.n(2)
	}
	m.lens[secThin] = max(1, n/5)
	m.lens[secReturn] = max(2, n/5)
	m.lens[secHold] = max(1, n-m.lens[secThin]-m.lens[secReturn])
	m.sec, m.secPos = secHold, 0
	if first {
		m.sec = secBuild
	}
}

// applyArc takes layers out by the section: a build brings the dropped
// layers in one by one, last-listed first; a thin takes out the first listed
// and halves the next; a break leaves only the anchor.
func (m *motifState) applyArc(t *Temperament, pats *[nTracks][rows]trackRow, drums *[nDrums][rows]drumHit) {
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

// decideLead repeats, mutates or redraws the lead's line, and says which.
func (m *motifState) decideLead(p *Params, pat int, pats *[nTracks][rows]trackRow, r *rng, rate float64) string {
	x := r.f()
	switch {
	case x < rate*0.25:
		m.lead = freshLead(p, pat, pats)
		return "redraw"
	case x < rate:
		return m.lead.mutate(p, r)
	}
	return "repeat"
}

func (m *motifState) decidePad(p *Params, pat int, pats *[nTracks][rows]trackRow, r *rng, rate float64) string {
	x := r.f()
	switch {
	case x < rate*0.25:
		m.pad = freshPad(p, pat, pats)
		return "redraw"
	case x < rate:
		// a strike in or out toward the pad's density, or a strike revoiced
		k := r.n(2)
		want := p.PadDensity * 2
		count := 0
		for _, on := range m.pad.on {
			if on {
				count++
			}
		}
		switch {
		case float64(count) < want-0.5 && !m.pad.on[k]:
			m.pad.on[k], m.pad.voice[k] = true, int8(r.n(3))
			return "add"
		case float64(count) > want+0.5 && m.pad.on[k]:
			m.pad.on[k] = false
			return "drop"
		case m.pad.on[k]:
			m.pad.voice[k] = int8((int(m.pad.voice[k]) + 1 + r.n(2)) % 3)
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

// patternChord is the chord buildPatterns draws for a pattern: its first
// draw, repeated here so a line in degrees can be read back from the notes.
// TestPatternChordMatches holds the two together.
func patternChord(p Params, pat int) int {
	r := rng{p.Seed ^ uint64(pat)*0x9e3779b97f4a7c15 | 1}
	return p.Root + []int{0, 0, scaleTone(p.Scale, -2), scaleTone(p.Scale, -4)}[r.n(4)]
}

// freshLead reads the generator's lead line from a pattern back into degrees.
func freshLead(p *Params, pat int, pats *[nTracks][rows]trackRow) leadLine {
	var l leadLine
	if p.LeadName == "" {
		return l
	}
	chord := patternChord(*p, pat)
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

func freshPad(p *Params, pat int, pats *[nTracks][rows]trackRow) padLine {
	var l padLine
	chord := patternChord(*p, pat)
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

// MotifTrace reports where the arc is and what the last boundary did to the
// lead and pad lines. It reads the audio thread's state unsynchronised: for an
// offline render that calls it between Reads, not for a live host.
func (s *Synth) MotifTrace() (sec string, phrase int, lead, pad string) {
	m := &s.motif
	if !m.started {
		return "off", 0, "", ""
	}
	return m.sec.String(), m.phrase, m.lastLead, m.lastPad
}
