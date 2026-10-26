package engine

import "math"

// line is one parameter's response to level: base + step*level, clamped.
// The clamps are ±Inf where a mood leaves a parameter unbounded, and
// math.Max and math.Min pass a value through those exactly.
type line struct{ base, step, lo, hi float64 }

func lin(base, step float64) line { return line{base, step, math.Inf(-1), math.Inf(1)} }

func (l line) atLeast(lo float64) line { l.lo = lo; return l }
func (l line) atMost(hi float64) line  { l.hi = hi; return l }

// at evaluates the line at a level. The float64 conversion stops the compiler
// fusing the multiply-add, which Go may do on arm64; unfused, the result is
// the same on every architecture.
func (l line) at(x float64) float64 {
	return math.Min(l.hi, math.Max(l.lo, l.base+float64(l.step*x)))
}

// mood is a patch bank plus a point in parameter space, and how that point
// moves as the level rises. Tension works the same in every mood, so it is
// not in the table: tempo up, pulse in, the second drone sliding to a tritone
// and a minor ninth, the calm layers thinning, and drums, where a mood has
// them, busier and harder.
type mood struct {
	name     string
	scales   [][]int // indexed by level/2; the last one holds for higher levels
	bpm      line
	rootBase int
	rootStep int
	drone    line
	fifth    line
	pad      line
	bell     line
	air      line
	calm     int // the second drone's interval from the chord at low tension; positive floats above it
	bellLift int
	palette  Palette
	leads    []string // the seed picks one; repeats are the weights

	// Drums, for rhythmic moods; the zero values leave them off.
	grooves []string // grooveLib names; the seed picks one
	drums   line     // drum gain
	busy    line     // chance each ? step plays
	swing   float64

	// open is how far the svf filters open with level, -1..1: up to two
	// octaves of cutoff either way. The zero line leaves them where the
	// patches set them.
	open line

	// tenseDrums brings in drums that the level leaves silent: from a
	// quarter tension they rise to this gain at full, so a fight starts a
	// beat under a mood that has none at rest.
	tenseDrums float64
}

var moods = []mood{
	// Depth darkens: dorian -> aeolian -> phrygian, slower, lower, thicker.
	{
		name:     "dungeon",
		scales:   scalesByDarkness,
		bpm:      lin(66, -3).atLeast(46),
		rootBase: 57, rootStep: -1,
		drone:   lin(0.55, 0.06).atMost(0.9),
		fifth:   lin(0, 0.2).atMost(0.7),
		pad:     lin(0.6, -0.05),
		bell:    lin(0.16, -0.02).atLeast(0.04),
		air:     lin(0.25, 0.05).atMost(0.6),
		calm:    -5, // perfect fifth above the low drone
		palette: dungeonPalette,
		leads:   dungeonLeads,
	},
	// The bright mirror: elevation raises and opens (mixolydian -> major ->
	// lydian, higher root, denser lifted bells, breeze instead of rumble).
	{
		name:     "overworld",
		scales:   scalesByBrightness,
		bpm:      lin(74, 3),
		rootBase: 62, rootStep: 1,
		drone:    lin(0.25, 0.03),
		fifth:    lin(0.3, 0.05),
		pad:      lin(0.7, 0),
		bell:     lin(0.2, 0.04),
		air:      lin(0.15, 0.02),
		calm:     -8, // major third above the low drone: sunshine
		bellLift: 12,
		palette:  overworldPalette,
		leads:    overworldLeads,
	},
	// Warm, grounded and sunny: overworld without the floor. The drone is
	// near silent, so nothing sits under the bed; the root starts high, bells
	// come dense and an octave up like birdsong, and the second drone sings a
	// major sixth over the first. Level is the climb through the meadow: a
	// little faster and busier, turning lydian at the top.
	{
		name:     "meadow",
		scales:   scalesForMeadow,
		bpm:      lin(88, 2).atMost(100),
		rootBase: 67, rootStep: 0,
		drone:    lin(0.12, 0),
		fifth:    lin(0.25, 0.03).atMost(0.4),
		pad:      lin(0.5, 0),
		bell:     lin(0.3, 0.03).atMost(0.45),
		air:      lin(0.2, 0.02).atMost(0.3),
		calm:     -3, // major sixth above the low drone
		bellLift: 12,
		palette:  overworldPalette,
		leads:    meadowLeads,
	},
	// Bright by being thin rather than warm: no drone at all, a slow lydian
	// sky of veil pads smeared through the delay, the second voice a fifth
	// above the chord, and only the odd chime. Level climbs: the root rises
	// and the wash thickens.
	{
		name:     "aurora",
		scales:   scalesForAurora,
		bpm:      lin(52, 1).atMost(58),
		rootBase: 69, rootStep: 1,
		drone:   lin(0, 0),
		fifth:   lin(0.3, 0.02).atMost(0.4),
		pad:     lin(0.75, 0.02).atMost(0.85),
		bell:    lin(0.08, 0.01).atMost(0.14),
		air:     lin(0.3, 0.03).atMost(0.45),
		calm:    7, // a fifth above the chord, floating over it
		palette: auroraPalette,
		leads:   auroraLeads,
	},
	// The first mood with drums: a warm daytime village, mixolydian turning
	// major, a light shuffle under kalimba and pluck. Level is the day
	// building to a festival: the shaker fills in, the drums come up.
	{
		name:     "village",
		scales:   scalesForVillage,
		bpm:      lin(100, 2).atMost(112),
		rootBase: 62, rootStep: 0,
		drone:    lin(0.15, 0),
		fifth:    lin(0.25, 0),
		pad:      lin(0.55, 0),
		bell:     lin(0.22, 0.02).atMost(0.32),
		air:      lin(0.1, 0),
		calm:     -8, // major third above the low drone
		bellLift: 12,
		palette:  overworldPalette,
		leads:    villageLeads,
		grooves:  []string{"amble", "amble", "skip", "stomp"},
		drums:    lin(0.55, 0.05).atMost(0.8),
		busy:     lin(0.2, 0.12).atMost(0.8),
		swing:    0.1,
	},
	// Industrial, after the bed of "Hurt": detuned saws grinding through
	// resonant filters that never sit still, a hiss bed played like an
	// instrument, a tritone where other moods have a fifth, and the odd
	// clang. Level sinks it: slower, lower, phrygian turning locrian, the
	// noise and the second drone coming up. Its tension pulse is a filter
	// envelope with teeth rather than a heartbeat.
	{
		name:     "rust",
		scales:   scalesForRust,
		bpm:      lin(58, -2).atLeast(44),
		rootBase: 45, rootStep: -1,
		drone:   lin(0.6, 0.04).atMost(0.85),
		fifth:   lin(0.15, 0.08).atMost(0.55),
		pad:     lin(0.35, 0.03).atMost(0.6),
		bell:    lin(0.06, 0),
		air:     lin(0.35, 0.05).atMost(0.7),
		calm:    -6, // a tritone from the start
		palette: rustPalette,
		leads:   rustLeads,
		open:    lin(0, -0.12), // the filters close as it sinks
		// no drums at rest; from a quarter tension the machine comes in
		grooves:    []string{"machine"},
		drums:      lin(0, 0),
		busy:       lin(0.3, 0.05).atMost(0.6),
		tenseDrums: 0.75,
	},
	// Rust's rhythmic sibling, after "Closer": the same saws, hiss and
	// industrial kit, but the machine groove is the floor rather than the
	// threat, playing from the first bar with a little swing in it. The
	// harmony holds a plain fifth so the beat carries the menace until
	// tension sours it. Level stokes it: faster, the drums up and busier,
	// the filters opening where rust's close, dorian darkening to phrygian.
	{
		name:     "forge",
		scales:   scalesByDarkness,
		bpm:      lin(88, 2).atMost(98),
		rootBase: 45, rootStep: 0,
		drone:   lin(0.45, 0.02).atMost(0.55),
		fifth:   lin(0.15, 0.03).atMost(0.3),
		pad:     lin(0.3, 0),
		bell:    lin(0.04, 0),
		air:     lin(0.25, 0.03).atMost(0.4),
		calm:    -5, // perfect fifth above the low drone
		palette: rustPalette,
		leads:   rustLeads,
		open:    lin(0, 0.08),
		grooves: []string{"machine"},
		drums:   lin(0.55, 0.03).atMost(0.75),
		busy:    lin(0.35, 0.05).atMost(0.6),
		swing:   0.06,
	},
	// Candidates for later moods, on the dark-to-bright axis:
	//   - Past dungeon, darker (Void, Below or Hades): lower and slower
	//     than depth reaches today, perhaps with a new low patch.
	//   - Stratosphere, a sibling of aurora: thinner still, with no lead.
	// The table cannot express pentatonic scales yet: the sequencer and grid
	// assume seven degrees.
}

// MoodNames lists the moods DeriveMood knows, darkest first.
func MoodNames() []string {
	names := make([]string, len(moods))
	for i, m := range moods {
		names[i] = m.name
	}
	return names
}

// DeriveMood maps game state to music for a named mood: level says where you
// are, tension (0..1) says what is happening. Tension 0 reproduces the
// pre-tension output exactly, because it takes no extra rng draws. It reports
// false for a mood it does not know.
func DeriveMood(name string, seed uint64, level int, tension float64) (Params, bool) {
	for i := range moods {
		if moods[i].name == name {
			return moods[i].derive(seed, level, tension), true
		}
	}
	return Params{}, false
}

func (m *mood) derive(seed uint64, level int, tension float64) Params {
	r := rng{seed ^ uint64(level)*0x9e3779b97f4a7c15 | 1}
	l := float64(level)
	t := math.Max(0, math.Min(1, tension))
	// Field order matters: the BPM jitter is drawn before the root offset.
	p := Params{
		BPM:         m.bpm.at(l) + float64(r.n(5)) + float64(30*t),
		Root:        m.rootBase + m.rootStep*level + []int{-2, 0, 0, 3}[r.n(4)],
		Scale:       m.scales[max(0, min(level/2, len(m.scales)-1))],
		DroneGain:   m.drone.at(l),
		FifthGain:   m.fifth.at(l) + float64(0.25*t),
		PadDensity:  m.pad.at(l) * (1 - float64(0.6*t)),
		BellDensity: m.bell.at(l) * (1 - float64(0.5*t)),
		AirGain:     m.air.at(l) + float64(0.1*t),
		PulseGain:   t,
		Grit:        t,
		Open:        math.Max(-1, math.Min(1, m.open.at(l))),
		FifthOffset: tenseOffset(m.calm, t),
		BellLift:    m.bellLift,
		Palette:     m.palette,
		Seed:        seed,
		DrumGain:    m.drums.at(l),
		DrumBusy:    m.busy.at(l),
		Swing:       m.swing,
	}
	pickLead(&p, m.leads)
	if len(m.grooves) > 0 {
		// Tension drives drums that are playing: the shaker fills in and the
		// hits land harder, so a fight builds a beat on top of the level's.
		// Drums at zero gain stay silent, and the gain keeps the mood's own
		// ceiling, which is set for balance against the bed; busy is a chance,
		// so tension may push it past the level's ceiling, up to certainty.
		// float64() keeps each product rounded on its own: without it arm64
		// fuses the multiply-add, and amd64 would derive other bits.
		if p.DrumGain > 0 {
			p.DrumGain = math.Min(m.drums.hi, p.DrumGain+float64(0.15*t))
			p.DrumBusy = min(1, p.DrumBusy+float64(0.4*t))
		} else if m.tenseDrums > 0 && t > 0.25 {
			p.DrumGain = float64(m.tenseDrums * float64((t-0.25)/0.75))
			p.DrumBusy = min(1, p.DrumBusy+float64(0.4*t))
		}
		pickGroove(&p, m.grooves)
	}
	return p
}

// DeriveDungeon is DeriveMood("dungeon", ...): deeper is darker.
func DeriveDungeon(seed uint64, depth int, tension float64) Params {
	p, _ := DeriveMood("dungeon", seed, depth, tension)
	return p
}

// DeriveOverworld is DeriveMood("overworld", ...): higher is brighter.
func DeriveOverworld(seed uint64, elevation int, tension float64) Params {
	p, _ := DeriveMood("overworld", seed, elevation, tension)
	return p
}
