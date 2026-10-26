package host

import (
	"testing"

	"github.com/nooga/let-go/pkg/api"

	"github.com/mparrett/undertone/engine"
	"github.com/mparrett/undertone/letgo"
)

// LoadEmbedded is the browser's whole load lane — GOOS=js has no filesystem,
// so if this breaks, the web demo dies at boot. Proving it natively keeps the
// failure findable by `go test` instead of by a blank phone screen.
func TestLoadEmbeddedRunsTheWholeLane(t *testing.T) {
	m := letgo.NewMusic(engine.DeriveDungeon(1, 0, 0))
	letgo.SetMusic(m)
	c, err := api.NewLetGo("embedtest")
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately no SetLoadPath: resolution must come from the embedded
	// sources alone, exactly as in the browser.
	if err := LoadEmbedded(c); err != nil {
		t.Fatal(err)
	}
	n, err := DemoTurns(c)
	if err != nil || n == 0 {
		t.Fatalf("demo/turns = %d, %v", n, err)
	}
	tension, line, err := DemoStep(c, m, 8, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if tension <= 0.5 || line == "" {
		t.Errorf("turn 9 (badly hurt) gave tension %v, line %q; the arc should be tightening there", tension, line)
	}
}
