// The descent demo's native front end embeds let-go and hands it the undertone engine through the audio
// surface sketch's v1 split: lg is the control language, Go is the DSP engine.
//
// Only control-rate data crosses the boundary — one parameter map per pattern
// (every few seconds) plus per-turn one-shot triggers, never per-sample PCM.
// That is the whole point: the per-sample unit VM runs millions of ops/second,
// so putting it in lg would walk straight into the boxed variadic call ABI
// (advisory #722). The layer that genuinely wants to be lg is the one above
// it: deriving music parameters and sfx triggers from game state, which
// happens at human rates and is where a game's taste lives.
//
// The natives and Music state come from undertone's letgo package; the
// demo driver lives in ../host so the browser front end (../web) runs the
// identical turn. This file is only the native CLI: flags and stdout.
//
// The natives registered are the sketch's proposed surface plus the one-shot
// lane:
//
//	undertone/music-start            begin streaming
//	undertone/music-stop             stop
//	undertone/set-parameters         one map, merged over current state, applied
//	                             at the next pattern boundary
//	undertone/trigger                one-shot sfx: a trigger map or a vector of
//	                             them (from sfx/for-events), fire-and-forget
//
// undertone/render-wav is a fifth, spike-only: it is how determinism gets checked
// without a sound card, not part of the proposed surface.
//
//	go run ./examples/descent/cli -demo        # the scripted arc, with sound
//	go run ./examples/descent/cli -demo -dry   # the tension trace only
//	go run ./examples/descent/cli -e '(do (ambience/score! 7 4 0.6) (undertone/render-wav "out.wav" 20.0))'
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/nooga/let-go/pkg/api"
	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/letgo"

	"github.com/mparrett/undertone/examples/descent/host"
)

func main() {
	loadPath := flag.String("load-path", "lg", "directory for any namespaces beyond the embedded ones")
	nsName := flag.String("ns", "[undertone.ambience :as ambience]", "lg namespace to require, or a libspec")
	expr := flag.String("e", "", "lg expression to run after the namespace loads")
	demo := flag.Bool("demo", false, "walk demo.lg's scripted descent-and-fight arc")
	dry := flag.Bool("dry", false, "with -demo: print the tension trace, make no sound")
	turnMS := flag.Int("turn-ms", 4500, "with -demo: real milliseconds per game turn")
	flag.Parse()

	// The base params supply the patch palette and a lead default; the script
	// is expected to overwrite every scalar via undertone/set-parameters.
	m := letgo.NewMusic(engine.DeriveDungeon(1, 0, 0))
	letgo.SetMusic(m)

	c, err := api.NewLetGo("undertone.host")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// undertone.ambience ships inside undertone, registered by its init, and
	// the demo's namespaces load from the embedded copies; the load path is
	// only for namespaces a caller adds.
	if err := host.LoadEmbedded(c); err != nil {
		fmt.Fprintln(os.Stderr, "lg:", err)
		os.Exit(1)
	}
	c.SetLoadPath([]string{*loadPath})
	if _, err := c.Run("(require '" + *nsName + ")"); err != nil {
		fmt.Fprintln(os.Stderr, "lg:", err)
		os.Exit(1)
	}
	if *demo {
		if err := runDemo(c, m, *dry, time.Duration(*turnMS)*time.Millisecond); err != nil {
			fmt.Fprintln(os.Stderr, "demo:", err)
			os.Exit(1)
		}
		return
	}
	if *expr != "" {
		v, err := c.Run(*expr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "lg:", err)
			os.Exit(1)
		}
		if v != vm.NIL {
			fmt.Println(v)
		}
	}
}

// runDemo walks demo.lg's scripted arc, one entry per turn. The per-turn
// mechanics live in host.DemoStep; this driver only owns stdout and the wait.
func runDemo(c *api.LetGo, m *letgo.Music, dry bool, turnDur time.Duration) error {
	if _, err := c.Run("(require 'demo)"); err != nil {
		return err
	}
	n, err := host.DemoTurns(c)
	if err != nil {
		return err
	}

	if !dry {
		m.Start()
		if !m.Started() {
			return fmt.Errorf("music-start did not open a player (see stderr)")
		}
		fmt.Println("playing the arc — parameter changes land at pattern boundaries (~8s),")
		fmt.Println("so the envelope is what makes each boundary sample representative.")
		fmt.Println()
	}

	tension := 0.0
	for i := 0; i < n; i++ {
		var line string
		tension, line, err = host.DemoStep(c, m, i, tension)
		if err != nil {
			return err
		}
		fmt.Println(line)
		if !dry {
			time.Sleep(turnDur)
		}
	}
	if !dry {
		m.Stop()
	}
	return nil
}
