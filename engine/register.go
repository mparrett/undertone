package engine

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
)

// Palettes and leads a game registered, beside the built-in ones. A game
// registers on its own thread while another derives, hence the lock; the
// audio thread never takes it, since Params carries the patches themselves.
// A registration replaces a name's patches with new ones and never changes
// them in place, so a Params holding the old ones plays them unchanged.
var (
	regMu      sync.RWMutex
	paletteReg = map[string]regPalette{}
	leadReg    = map[string]regLead{}
)

type regPalette struct {
	pal  Palette
	spec PaletteSpec // as it reads back: units filled in, default lanes marked
}

type regLead struct {
	def  leadDef
	spec LeadSpec
}

func lookupPalette(name string) (Palette, bool) {
	if p, ok := palettes[name]; ok {
		return p, true
	}
	regMu.RLock()
	defer regMu.RUnlock()
	r, ok := paletteReg[name]
	return r.pal, ok
}

func lookupLead(name string) (leadDef, bool) {
	if d, ok := leadLib[name]; ok {
		return d, true
	}
	regMu.RLock()
	defer regMu.RUnlock()
	r, ok := leadReg[name]
	return r.def, ok
}

// registeredNames lists a registry's names, sorted.
func registeredNames[T any](reg map[string]T) []string {
	regMu.RLock()
	defer regMu.RUnlock()
	names := make([]string, 0, len(reg))
	for n := range reg {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

var trackNames = [...]string{"drone", "fifth", "pad", "air", "pulse"}
var laneNames = [nDrums]string{"kick", "clap", "shaker"}

// tracks lists the spec's track patches in track order.
func (s *PaletteSpec) tracks() []*PatchSpec {
	return []*PatchSpec{&s.Drone, &s.Fifth, &s.Pad, &s.Air, &s.Pulse}
}

func (pl *Palette) tracks() []*Patch {
	return []*Patch{&pl.Drone, &pl.Fifth, &pl.Pad, &pl.Air, &pl.Pulse}
}

// RegisterPalette makes a spec a palette a mood can name (MoodSpec.Palette,
// :palette), replacing an earlier registration of that name. It reports an
// error, and registers nothing, for a built-in palette's name or a patch the
// synth could not run safely.
//
// A patch is its units, or with no units, the built-in patch its Name names
// ("pad", "kick"; PaletteSpecByName gives every built-in's names). A patch
// with both must build that built-in exactly, so a spec read from a built-in
// palette registers as it reads; one whose units were edited needs a new
// name, or none. The lead track's sound is a lead's, so a game's own is
// registered with RegisterLead. A kit lane left zero, or marked Default,
// plays the default kit's; a Default lane naming any other sound is refused.
//
// A music patch is held to a one-shot's bounds but two: its release may run
// to 10 s and its out gain to 4 (musicBounds), as the built-in beds need.
func RegisterPalette(s PaletteSpec) error {
	pl, spec, err := s.build()
	if err != nil {
		return fmt.Errorf("palette %q: %w", s.Name, err)
	}
	regMu.Lock()
	defer regMu.Unlock()
	paletteReg[s.Name] = regPalette{pl, spec}
	return nil
}

// build checks the spec and builds its palette, and the spec as it will
// read back.
func (s PaletteSpec) build() (Palette, PaletteSpec, error) {
	var pl Palette
	out := PaletteSpec{Name: s.Name}
	if !validName(s.Name) {
		return pl, out, fmt.Errorf("a palette needs a name of letters, digits and dashes, as a keyword")
	}
	if _, ok := palettes[s.Name]; ok {
		return pl, out, fmt.Errorf("a built-in palette has that name")
	}
	dst, read := pl.tracks(), out.tracks()
	for i, ps := range s.tracks() {
		p, err := ps.build()
		if err != nil {
			return pl, out, fmt.Errorf("%s: %w", trackNames[i], err)
		}
		*dst[i] = p
		*read[i] = PatchSpec{ps.Name, unitSpecs(p)}
	}
	// As in every built-in bank, so lg's partial :lead :none can put it
	// back without knowing which bank is playing (mergeParams).
	pl.Lead = bell
	for i, d := range s.Kit {
		k := drumKit[i]
		if d.Default || d.zero() && d.Note == 0 {
			// as read back, a default lane names the default kit's sound;
			// any other sound on it is a mistake, not a lane to drop
			if !d.zero() || d.Note != 0 {
				p, err := d.PatchSpec.build()
				if err != nil || !samePatch(p, k.patch) || d.Note != k.note {
					return pl, out, fmt.Errorf("kit %s: a Default lane plays the default kit's %s at %d; clear Default to play another", laneNames[i], laneNames[i], k.note)
				}
			}
			out.Kit[i] = DrumSpec{patchSpec(k.patch), k.note, true}
			continue
		}
		p, err := d.PatchSpec.build()
		if err != nil {
			return pl, out, fmt.Errorf("kit %s: %w", laneNames[i], err)
		}
		if d.Note < minNote || d.Note > maxNote {
			return pl, out, fmt.Errorf("kit %s: note %d: want %d to %d", laneNames[i], d.Note, minNote, maxNote)
		}
		pl.Kit[i] = drumSound{p, d.Note}
		out.Kit[i] = DrumSpec{PatchSpec{d.Name, unitSpecs(p)}, d.Note, false}
	}
	return pl, out, nil
}

// RegisterLead makes a spec a lead a mood can name (MoodSpec.Leads, :lead),
// replacing an earlier registration of that name. It reports an error, and
// registers nothing, for a built-in lead's name, a gate outside 1 to 16 rows,
// or a patch the synth could not run safely, held to RegisterPalette's
// bounds. A registered lead is unmeasured: the generator plays it at trim 1
// (leadGain), so its patch's out gain sets its level.
func RegisterLead(s LeadSpec) error {
	d, err := s.build()
	if err != nil {
		return fmt.Errorf("lead %q: %w", s.Name, err)
	}
	regMu.Lock()
	defer regMu.Unlock()
	leadReg[s.Name] = regLead{d, LeadSpec{PatchSpec{s.Name, unitSpecs(d.patch)}, s.Gate}}
	return nil
}

func (s LeadSpec) build() (leadDef, error) {
	if !validName(s.Name) {
		return leadDef{}, fmt.Errorf("a lead needs a name of letters, digits and dashes, as a keyword")
	}
	if s.Name == "none" { // lg's :lead :none, the absence of a lead
		return leadDef{}, fmt.Errorf("none means no lead")
	}
	if _, ok := leadLib[s.Name]; ok {
		return leadDef{}, fmt.Errorf("a built-in lead has that name")
	}
	if s.Gate < 1 || s.Gate > rows {
		return leadDef{}, fmt.Errorf("gate %d rows: want 1 to %d", s.Gate, rows)
	}
	p, err := buildPatch(s.Units, musicBounds)
	if err != nil {
		return leadDef{}, err
	}
	return leadDef{s.Name, p, s.Gate}, nil
}

func builtinPatch(name string) (Patch, bool) {
	for _, n := range namedPatches {
		if n.name == name {
			return n.p, true
		}
	}
	return nil, false
}

func (ps PatchSpec) zero() bool { return ps.Name == "" && len(ps.Units) == 0 }

// build builds a palette's patch. A built-in comes back as itself, not a
// copy: a voice sounding it carries on legato across a change to a palette
// that shares it (Voice.trigger, samePatch).
func (ps PatchSpec) build() (Patch, error) {
	bp, builtin := builtinPatch(ps.Name)
	if len(ps.Units) == 0 {
		if builtin {
			return bp, nil
		}
		return nil, fmt.Errorf("patch %q: want units, or the name of a built-in patch", ps.Name)
	}
	p, err := buildPatch(ps.Units, musicBounds)
	if err != nil {
		return nil, err
	}
	if builtin {
		if !reflect.DeepEqual(p, bp) {
			return nil, fmt.Errorf("the units differ from the built-in %q: rename the patch, or clear its name", ps.Name)
		}
		return bp, nil
	}
	return p, nil
}

func (ps PatchSpec) clone() PatchSpec {
	units := slices.Clone(ps.Units)
	for i := range units {
		units[i].Args = slices.Clone(units[i].Args)
	}
	return PatchSpec{ps.Name, units}
}

func (s PaletteSpec) clone() PaletteSpec {
	for _, ps := range s.tracks() {
		*ps = ps.clone()
	}
	for i := range s.Kit {
		s.Kit[i].PatchSpec = s.Kit[i].PatchSpec.clone()
	}
	return s
}

// EDN writes the spec as an lg map, the form undertone/register-palette
// takes. A patch that is a built-in is written as its name.
func (s PaletteSpec) EDN() string {
	var b strings.Builder
	fmt.Fprintf(&b, "{:name :%s", s.Name)
	for i, ps := range s.tracks() {
		fmt.Fprintf(&b, "\n :%s %s", trackNames[i], ps.edn(len(trackNames[i])+3))
	}
	b.WriteString("\n :kit [")
	for i, d := range s.Kit {
		if i > 0 {
			b.WriteString("\n       ")
		}
		if d.Default {
			b.WriteString(":default")
			continue
		}
		head := fmt.Sprintf("{:note %d :patch ", d.Note)
		fmt.Fprintf(&b, "%s%s}", head, d.edn(7+len(head)))
	}
	b.WriteString("]}")
	return b.String()
}

// EDN writes the spec as an lg map, the form undertone/register-lead takes.
func (s LeadSpec) EDN() string {
	return fmt.Sprintf("{:name :%s :gate %d\n :patch %s}", s.Name, s.Gate, unitsEDN(s.Units, "         "))
}

// edn writes a palette's patch, starting at column col: a built-in as its
// name, else its units, in a map with its name if it has one.
func (ps PatchSpec) edn(col int) string {
	if bp, ok := builtinPatch(ps.Name); ok {
		p, err := buildPatch(ps.Units, musicBounds)
		if len(ps.Units) == 0 || err == nil && reflect.DeepEqual(p, bp) {
			return ":" + ps.Name
		}
	}
	if ps.Name == "" {
		return unitsEDN(ps.Units, strings.Repeat(" ", col+1))
	}
	head := fmt.Sprintf("{:name :%s :patch ", ps.Name)
	return head + unitsEDN(ps.Units, strings.Repeat(" ", col+len(head)+1)) + "}"
}
