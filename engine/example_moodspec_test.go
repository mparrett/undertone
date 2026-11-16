package engine_test

import (
	"fmt"

	"github.com/mparrett/undertone/engine"
)

// A game's own mood: start from a built-in, change what differs, and derive
// parameters from it as from any mood. Palette names its patches, built-in
// or registered; PaletteNames, LeadNames and GrooveNames list what a spec
// can use.
func ExampleMoodSpec_Derive() {
	crypt, _ := engine.MoodByName("dungeon")
	crypt.Name = "crypt"
	crypt.BPM.Base -= 10                     // slower than the dungeon
	crypt.Leads = []string{"clang", "clang"} // only the eerie lead
	p, err := crypt.Derive(7, 3, 0.4)        // seed, level, tension
	if err != nil {
		panic(err)
	}
	// synth.SetParameters(p) plays it.
	fmt.Println(p.LeadName, p.BPM < 66)
	// Output: clang true
}

// A game's own palette: start from a built-in, swap in a patch of its own,
// register it, and name it in a mood. A patch left with its built-in name
// stays that built-in; an edited one takes a new name, or none.
func ExampleRegisterPalette() {
	ember, _ := engine.PaletteSpecByName("rust")
	ember.Name = "ember"
	ember.Drone = engine.PatchSpec{Name: "ember-drone", Units: []engine.UnitSpec{
		{Op: "env", Args: []float64{4, 2, 1, 8}}, // an 8 s release: a bed's, not a shot's
		{Op: "osc", Shape: "saw", Args: []float64{0, 0, 1}},
		{Op: "mul"},
		{Op: "lpf", Args: []float64{220}},
		{Op: "out", Args: []float64{0.4, 0.5}},
	}}
	if err := engine.RegisterPalette(ember); err != nil {
		panic(err)
	}
	glass, _ := engine.LeadSpecByName("chime")
	glass.Name = "glass"
	if err := engine.RegisterLead(glass); err != nil {
		panic(err)
	}
	forge, _ := engine.MoodByName("rust")
	forge.Palette, forge.Leads = "ember", []string{"glass"}
	p, err := forge.Derive(7, 3, 0.4)
	if err != nil {
		panic(err)
	}
	fmt.Println(p.LeadName)
	fmt.Println(ember.EDN())
	// Output:
	// glass
	// {:name :ember
	//  :drone {:name :ember-drone :patch [[:env 4.0 2.0 1.0 8.0]
	//                                     [:osc 0.0 :saw 0.0 1.0]
	//                                     [:mul]
	//                                     [:lpf 220.0]
	//                                     [:out 0.4 0.5]]}
	//  :fifth :smear
	//  :pad :smear
	//  :bell :bell
	//  :air :hiss
	//  :pulse :grind
	//  :kit [{:note 33 :patch :slam}
	//        {:note 50 :patch :clank}
	//        {:note 84 :patch :tick}]}
}
