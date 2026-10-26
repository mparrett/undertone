// lghost embeds let-go with the undertone binding linked in: lg is the control
// language, Go is the DSP engine.
//
// Only control-rate data crosses the boundary — one parameter map per pattern
// (every few seconds) plus one-shot triggers, never per-sample PCM. That is
// the whole point: the per-sample unit VM runs millions of ops/second, so
// putting it in lg would walk straight into the boxed variadic call ABI. The
// layer that genuinely wants to be lg is the one above it: deriving music
// parameters and sfx triggers from game state, which happens at human rates.
//
// The undertone namespace comes from the letgo package's init (see its doc for the
// natives) and so does undertone.ambience, which the CLI requires as
// ambience by default. This file is only the native CLI: flags, an optional
// load path for your own namespaces, and stdout. An lg runtime built by lgx with the letgo package as a Go dep
// does the same job.
//
//	go run ./cmd/lghost -e '(do (ambience/score! 7 4 0.6) (undertone/music-start) (sleep 60000))'
//	go run ./cmd/lghost -e '(do (ambience/score! 7 4 0.6) (undertone/render-wav "out.wav" 20.0))'
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/nooga/let-go/pkg/api"
	"github.com/nooga/let-go/pkg/vm"

	"github.com/mparrett/undertone/letgo"
)

func main() {
	loadPath := flag.String("load-path", "", "directory holding your own lg namespaces (undertone's are built in)")
	nsName := flag.String("ns", "[undertone.ambience :as ambience]", "lg namespace to require, or a libspec")
	expr := flag.String("e", "", "lg expression to run after the namespace loads")
	flag.Parse()

	_ = letgo.CurrentMusic() // the import registers the undertone namespace

	c, err := api.NewLetGo("undertone.host")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// api.Run compiles a single form, so the whole-file lane is require, not a
	// read-and-eval loop.
	if *loadPath != "" {
		c.SetLoadPath([]string{*loadPath})
	}
	if _, err := c.Run("(require '" + *nsName + ")"); err != nil {
		fmt.Fprintln(os.Stderr, "lg:", err)
		os.Exit(1)
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
