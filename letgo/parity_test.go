package letgo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mparrett/undertone/engine"
)

// lgSide is the decision, made when an engine export lands, of what lg has
// for it: "native:<name>", an undertone/ function; "key:<name>", a
// set-parameters key; or "go:<reason>", Go only on purpose. A new export
// fails TestLgParity until it has a line here, so the lg side is decided
// with the Go one rather than found missing by a game later.
var lgSide = map[string]string{
	// the synth
	"Synth.Cut":              "native:cut",
	"Synth.EchoLevel":        "go:a wet-bus meter for the studio's echo display; lg meters with levels",
	"Synth.EndTheme":         "native:end-theme",
	"Synth.Frames":           "go:output plumbing; position reports :frame",
	"Synth.GlideMusicGain":   "native:glide-music-gain",
	"Synth.GlideSfxGain":     "native:glide-sfx-gain",
	"Synth.LayerLevels":      "native:layer-levels",
	"Synth.Levels":           "native:levels",
	"Synth.NoteEvents":       "native:note-events",
	"Synth.PlayTheme":        "native:play-theme",
	"Synth.Position":         "native:position",
	"Synth.PositionAt":       "go:output plumbing, for a host following the row heard at its own clock",
	"Synth.Read":             "go:the PCM an output pulls",
	"Synth.RenderStats":      "native:audio-stats",
	"Synth.ReviseTheme":      "native:revise-theme",
	"Synth.Rewind":           "go:an editor's from-the-top button (the grid page); a game restarts with play-theme",
	"Synth.RowAt":            "go:output plumbing behind position",
	"Synth.RunStart":         "go:output plumbing behind position's :run",
	"Synth.SetGrid":          "go:the grid page's live editing; a game plays authored music as a theme",
	"Synth.SetMusicGain":     "native:set-music-gain",
	"Synth.SetParameters":    "native:set-parameters",
	"Synth.SetParametersNow": "native:set-parameters-now",
	"Synth.SetSfxGain":       "native:set-sfx-gain",
	"Synth.ThemePlaying":     "native:theme-playing?",
	"Synth.Trigger":          "native:trigger",
	"Synth.TriggerShot":      "native:trigger",

	// music
	"ApplyBass":       "key:bass",
	"ApplyLead":       "key:lead",
	"DeriveMood":      "native:derive-spec",
	"DeriveDungeon":   "native:derive-spec",
	"DeriveOverworld": "native:derive-spec",
	"MoodByName":      "native:moods-edn",
	"MoodNames":       "native:moods-edn",
	"MoodSpec.Derive": "native:derive-spec",
	"MoodSpec.EDN":    "native:moods-edn",
	"NewSynth":        "native:music-start",
	"DegreeNote":      "go:the grid page's note names; lg writes themes in degrees",
	"GridBases":       "go:the grid page's note names; lg writes themes in degrees",
	"SeedGrid":        "go:the grid page's seeded starting pattern",
	"GrooveNames":     "go:listing for the studio's pickers; lg names a groove in a mood",
	"LeadNames":       "go:listing for the studio's pickers; an unknown :lead or :bass errors with the list",
	"LaneNames":       "go:labels for NoteEvent.Lane; note-events gives lanes as keywords",
	"LayerNames":      "go:labels for Layers bits; lg names layers as keywords",

	// registries
	"RegisterShot":      "native:register-shot",
	"RegisterPalette":   "native:register-palette",
	"RegisterLead":      "native:register-lead",
	"HasShot":           "go:lookup for Go hosts; trigger of an unknown shot is dropped by contract",
	"ShotByName":        "go:read-back for the studio; lg registers what it wrote",
	"ShotNames":         "go:listing for the studio's pickers",
	"ShotSpec.EDN":      "go:writes the map register-shot takes, for the studio's export",
	"PaletteByName":     "key:palette",
	"PaletteNames":      "go:listing for the studio's pickers",
	"PaletteSpecByName": "go:read-back for the studio; lg starts from a palette with register-palette's :base",
	"PaletteSpec.EDN":   "go:writes the map register-palette takes, for the studio's export",
	"LeadSpecByName":    "go:read-back for the studio",
	"LeadSpec.EDN":      "go:writes the map register-lead takes, for the studio's export",

	// rendering and measuring
	"ReadUntilSilent":        "native:render-wav",
	"WriteWAV":               "native:render-wav",
	"WritePCMWAV":            "native:render-wav",
	"Take.Render":            "go:cached renders for Go hosts and tests; lg renders with render-wav",
	"Take.RenderUntilSilent": "go:cached renders for Go hosts and tests; lg renders with render-wav",
	"Take.Hash":              "go:the cache key behind Take.Render",
	"Take.UntilSilentHash":   "go:the cache key behind Take.RenderUntilSilent",
	"Measure":                "go:offline loudness for the sfx board and tests",
	"MeasurePCM":             "go:offline loudness for the sfx board and tests",
	"StackTrace":             "go:a patch's stack depth, for the studio's patch checker",
	"NoteKind.String":        "go:fmt.Stringer",
}

// paramKey is the set-parameters key for each Params field, or "go:<reason>".
var paramKey = map[string]string{
	"BPM": "bpm", "Root": "root", "Scale": "scale",
	"DroneGain": "drone-gain", "FifthGain": "fifth-gain", "PadDensity": "pad-density",
	"LeadDensity": "lead-density", "AirGain": "air-gain", "PulseGain": "pulse-gain",
	"FifthOffset": "fifth-offset", "LeadLift": "lead-lift",
	"LeadName": "lead", "LeadGate": "lead", // a lead brings its gate
	"Palette": "palette", "Seed": "seed", "Groove": "groove",
	"DrumGain": "drum-gain", "DrumBusy": "drum-busy", "Swing": "swing",
	"Grit": "grit", "Open": "open", "Room.Size": "room-size", "Room.Damp": "room-damp",
	"Mute": "mute", "LeadDB": "lead-db", "PadDB": "pad-db", "BassDB": "bass-db", "Feel": "feel",
	"MoodLeadDB": "mood-lead-db", "MoodPadDB": "mood-pad-db",
}

// setOnly are the set-parameters keys that set no one field: :mood and :bass
// choose patches in Palette.
var setOnly = []string{"mood", "bass"}

// broughtBy are the fields a key sets only as part of something else, so
// setting the key to a number does not reach them: a lead brings its gate.
var broughtBy = map[string]bool{"LeadGate": true}

// engineExports lists the engine's exported functions and methods: "F" for
// a package function, "T.M" for a method.
func engineExports(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob("../engine/*.go")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	fset := token.NewFileSet()
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() {
				continue
			}
			name := fn.Name.Name
			if fn.Recv != nil {
				typ := fn.Recv.List[0].Type
				if st, ok := typ.(*ast.StarExpr); ok {
					typ = st.X
				}
				id, ok := typ.(*ast.Ident)
				if !ok || !id.IsExported() {
					continue
				}
				name = id.Name + "." + name
			}
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// Every engine export has a decided lg side, every native that side names
// exists, and every set-parameters key it names is one Params has.
func TestLgParity(t *testing.T) {
	c, _ := lgRuntime(t)
	exports := engineExports(t)
	if len(exports) < 20 {
		t.Fatalf("found only %d engine exports: is ../engine still the engine?", len(exports))
	}
	keys := map[string]bool{}
	for _, k := range paramKey {
		if !strings.HasPrefix(k, "go:") {
			keys[k] = true
		}
	}
	for _, k := range setOnly {
		keys[k] = true
	}
	seen := map[string]bool{}
	for _, e := range exports {
		seen[e] = true
		side, ok := lgSide[e]
		if !ok {
			t.Errorf("engine.%s has no lg side: add it to lgSide in parity_test.go, as an undertone/ native, a set-parameters key, or go-only with the reason", e)
			continue
		}
		kind, what, _ := strings.Cut(side, ":")
		switch kind {
		case "native":
			if _, err := c.Run("undertone/" + what); err != nil {
				t.Errorf("engine.%s: lg side undertone/%s does not exist: %v", e, what, err)
			}
		case "key":
			if !keys[what] {
				t.Errorf("engine.%s: lg side :%s is not a set-parameters key in paramKey", e, what)
			}
		case "go":
			if what == "" {
				t.Errorf("engine.%s: go-only needs its reason", e)
			}
		default:
			t.Errorf("engine.%s: lg side %q: want native:, key: or go:", e, side)
		}
	}
	for e := range lgSide {
		if !seen[e] {
			t.Errorf("lgSide lists engine.%s, which the engine no longer exports", e)
		}
	}
}

// Every Params field has a set-parameters key, or is go-only with a reason,
// and each numeric one is the key that sets it: an lg map with just that key
// changes just that field.
func TestParamsParity(t *testing.T) {
	c, m := lgRuntime(t)
	fields := map[string]reflect.Value{}
	var walk func(prefix string, v reflect.Value)
	walk = func(prefix string, v reflect.Value) {
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if !f.IsExported() {
				continue
			}
			if f.Type.Kind() == reflect.Struct && f.Type.Name() == "Room" {
				walk(prefix+f.Name+".", v.Field(i))
				continue
			}
			fields[prefix+f.Name] = v.Field(i)
		}
	}
	walk("", reflect.ValueOf(&m.params).Elem())
	for name := range fields {
		if _, ok := paramKey[name]; !ok {
			t.Errorf("Params.%s has no set-parameters key: add one, or list it in paramKey as go:<reason>", name)
		}
	}
	for name := range paramKey {
		if _, ok := fields[name]; !ok {
			t.Errorf("paramKey lists Params.%s, which Params no longer has", name)
		}
	}
	names := make([]string, 0, len(paramKey))
	for name := range paramKey {
		names = append(names, name)
	}
	sort.Strings(names)
	for i, name := range names {
		key := paramKey[name]
		if reason, ok := strings.CutPrefix(key, "go:"); ok {
			if reason == "" {
				t.Errorf("Params.%s: go-only needs its reason", name)
			}
			continue
		}
		if broughtBy[name] {
			continue
		}
		// a value no other key was given, so a key that writes a second
		// field shows as that field changing too
		var lit string
		var want reflect.Value
		switch fields[name].Kind() {
		case reflect.Float64:
			x := float64(i+1) / 64
			lit, want = strconv.FormatFloat(x, 'f', -1, 64), reflect.ValueOf(x)
		case reflect.Int:
			lit, want = strconv.Itoa(i+2), reflect.ValueOf(i+2)
		case reflect.Uint64:
			lit, want = strconv.Itoa(i+2), reflect.ValueOf(uint64(i+2))
		default:
			continue // a name, a list, a bank or a layer set: their own tests read them
		}
		before := m.params
		if _, err := c.Run("(undertone/set-parameters {:" + key + " " + lit + "})"); err != nil {
			t.Errorf(":%s: %v", key, err)
			continue
		}
		expect := before
		fieldOf(&expect, name).Set(want)
		if !reflect.DeepEqual(m.params, expect) {
			t.Errorf("set-parameters {:%s %s}: Params.%s is %v, and the rest should not move\ngot  %+v\nwant %+v",
				key, lit, name, fields[name].Interface(), m.params, expect)
		}
	}
}

// fieldOf is Params field name, dotted for Room's.
func fieldOf(p *engine.Params, name string) reflect.Value {
	v := reflect.ValueOf(p).Elem()
	for _, part := range strings.Split(name, ".") {
		v = v.FieldByName(part)
	}
	return v
}
