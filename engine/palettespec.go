package engine

// PatchSpec is a patch as data, in the unit form a ShotSpec uses, under the
// name the engine knows it by.
type PatchSpec struct {
	Name  string
	Units []UnitSpec
}

// DrumSpec is one lane of a drum kit: its patch and the note it plays.
type DrumSpec struct {
	PatchSpec
	Note int
}

// PaletteSpec is a palette as data: the patch each track plays and the drum
// kit's lanes (kick, clap, shaker). It reads a built-in palette; nothing
// registers one yet. Letting a mood bring its own (#36) would start from
// this form, as RegisterShot starts from ShotSpec.
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

// PaletteSpecByName returns a built-in palette as data. A lane the palette's
// kit leaves empty plays the default kit's (Params.sound), and its spec says so.
func PaletteSpecByName(name string) (PaletteSpec, bool) {
	pl, ok := PaletteByName(name)
	if !ok {
		return PaletteSpec{}, false
	}
	s := PaletteSpec{
		Name:  name,
		Drone: patchSpec(pl.Drone), Fifth: patchSpec(pl.Fifth), Pad: patchSpec(pl.Pad),
		Bell: patchSpec(pl.Bell), Air: patchSpec(pl.Air), Pulse: patchSpec(pl.Pulse),
	}
	for i, d := range pl.Kit {
		if d.patch == nil {
			d = drumKit[i]
		}
		s.Kit[i] = DrumSpec{patchSpec(d.patch), d.note}
	}
	return s, true
}

// LeadSpecByName returns a lead, one of LeadNames, as data.
func LeadSpecByName(name string) (LeadSpec, bool) {
	d, ok := leadLib[name]
	if !ok {
		return LeadSpec{}, false
	}
	return LeadSpec{patchSpec(d.patch), d.gate}, true
}
