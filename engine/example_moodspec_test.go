package engine_test

import (
	"fmt"

	"github.com/mparrett/undertone/engine"
)

// A game's own mood: start from a built-in, change what differs, and derive
// parameters from it as from any mood. The patches stay built-in, named by
// Palette; PaletteNames, LeadNames and GrooveNames list what a spec can use.
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
