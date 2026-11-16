package engine

// PatchSpec is a patch as data, in the unit form a ShotSpec uses, under the
// name the engine knows it by.
type PatchSpec struct {
	Name  string
	Units []UnitSpec
}

// DrumSpec is one lane of a drum kit: its patch and the note it plays.
// Default marks a lane the palette leaves to the default kit, which is what
// plays there (Params.sound) and what a change to the default kit reaches.
type DrumSpec struct {
	PatchSpec
	Note    int
	Default bool
}

// PaletteSpec is a palette as data: the patch each track plays and the drum
// kit's lanes (kick, clap, shaker). PaletteSpecByName reads one, and
// RegisterPalette makes one a mood can name, as RegisterShot does a shot.
//
// Bell is the exception to "plays": it is the bell track's patch before the
// lead is picked. A mood with leads replaces it with the seed's pick
// (ApplyLead; LeadSpecByName reads that patch), and a mood without leads
// plays the track at zero gain (leadTrim), so as built in, it is never heard.
type PaletteSpec struct {
	Name                                string
	Drone, Fifth, Pad, Bell, Air, Pulse PatchSpec
	Kit                                 [nDrums]DrumSpec
}

// LeadSpec is a lead as data: the patch the seed's pick puts in the bell's
// slot, and its gate in grid rows (sustained leads hold more than one).
type LeadSpec struct {
	PatchSpec
	Gate int
}

// namedPatches names the built-in patches. A Patch is a slice with no name
// of its own, and palettes and leads share the package's vars, so the name
// is found by the backing array.
var namedPatches = []struct {
	name string
	p    Patch
}{
	{"drone", drone}, {"pad", pad}, {"bell", bell}, {"air", air}, {"pulse", pulse},
	{"bed", bed}, {"breeze", breeze}, {"veil", veil}, {"haze", haze},
	{"rust", rust}, {"smear", smear}, {"hiss", hiss}, {"grind", grind},
	{"abyss", abyss}, {"murk", murk}, {"rumble", rumble},
	{"chime", chime}, {"clang", clang}, {"kalimba", kalimba}, {"reed", reed}, {"pluck", pluck},
	{"kick", kick}, {"clap", clap}, {"shaker", shaker}, {"slam", slam}, {"clank", clank}, {"tick", tick},
}

func patchName(p Patch) string {
	for _, n := range namedPatches {
		if len(n.p) == len(p) && len(p) > 0 && &n.p[0] == &p[0] {
			return n.name
		}
	}
	return ""
}

// unitSpecs converts a patch to its units as data.
func unitSpecs(p Patch) []UnitSpec {
	out := make([]UnitSpec, len(p))
	for i, u := range p {
		us := UnitSpec{Op: opNames[u.op], Args: append([]float64(nil), u.p[:opArgs[u.op]]...)}
		if u.op == opOsc {
			us.Shape = shapeNames[int(u.p[1])]
			us.Args = []float64{u.p[0], u.p[2], u.p[3]}
		}
		out[i] = us
	}
	return out
}

func patchSpec(p Patch) PatchSpec { return PatchSpec{patchName(p), unitSpecs(p)} }

// PaletteSpecByName returns a built-in or registered palette as data. A lane
// the palette's kit leaves empty plays the default kit's (Params.sound); its
// spec holds that lane, marked Default.
func PaletteSpecByName(name string) (PaletteSpec, bool) {
	pl, ok := palettes[name]
	if !ok {
		regMu.RLock()
		defer regMu.RUnlock()
		r, ok := paletteReg[name]
		return r.spec.clone(), ok
	}
	s := PaletteSpec{
		Name:  name,
		Drone: patchSpec(pl.Drone), Fifth: patchSpec(pl.Fifth), Pad: patchSpec(pl.Pad),
		Bell: patchSpec(pl.Bell), Air: patchSpec(pl.Air), Pulse: patchSpec(pl.Pulse),
	}
	for i, d := range pl.Kit {
		def := d.patch == nil
		if def {
			d = drumKit[i]
		}
		s.Kit[i] = DrumSpec{patchSpec(d.patch), d.note, def}
	}
	return s, true
}

// LeadSpecByName returns a lead, one of LeadNames, as data.
func LeadSpecByName(name string) (LeadSpec, bool) {
	if d, ok := leadLib[name]; ok {
		return LeadSpec{patchSpec(d.patch), d.gate}, true
	}
	regMu.RLock()
	defer regMu.RUnlock()
	r, ok := leadReg[name]
	return LeadSpec{r.spec.clone(), r.spec.Gate}, ok
}
