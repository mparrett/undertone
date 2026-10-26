//go:build js && lg

package main

import (
	"encoding/json"
	"sync/atomic"
	"syscall/js"

	"github.com/mparrett/undertone/examples/grid/lgmutate"
)

// Built with -tags lg, the page gains a mutate button whose transform is lg
// code (lgmutate/mutate.lg). Opt-in because let-go's runtime takes the page
// from about 5 MB to about 30.
func init() {
	extraAPI = func(api map[string]any) {
		var m atomic.Pointer[lgmutate.Mutator]
		go func() {
			mu, err := lgmutate.New()
			if err != nil {
				println("grid: lg mutate unavailable:", err.Error())
				return
			}
			m.Store(mu)
		}()
		api["mutate"] = js.FuncOf(func(_ js.Value, a []js.Value) any {
			mu := m.Load()
			if mu == nil {
				return nil // still booting
			}
			var j gridJSON
			if json.Unmarshal([]byte(a[0].String()), &j) != nil {
				return nil
			}
			out, err := mu.Mutate(j.grid(), a[1].Int())
			if err != nil {
				println("grid: mutate:", err.Error())
				return nil
			}
			return toJSON(out)
		})
	}
}
