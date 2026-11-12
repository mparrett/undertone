package letgo

import (
	"path/filepath"
	"testing"
)

// let-go builds a vector past 32 elements as a PersistentVector (mapv, into,
// conj), not an ArrayVector. Every vector undertone reads must take both: a
// trigger list or render script of 33 used to be turned away whole.
func TestVectorsPast32ElementsAreRead(t *testing.T) {
	c := renderLg(t, "longvectors")
	v, err := c.Run(`(mapv (fn [i] {:shot :pluck :note (+ 40 (mod i 24)) :delay-ms (* 20 i)}) (range 40))`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := asVec(v); !ok {
		t.Fatalf("%T not read as a vector", v)
	}
	if n := len(shots(v)); n != 40 {
		t.Errorf("a 40-trigger list read as %d shots, want 40", n)
	}
	path := filepath.Join(t.TempDir(), "long.wav")
	if _, err := c.Run(`(undertone/render-wav "` + path + `" 2.0
	                      (mapv (fn [i] [(* 0.04 i) :trigger {:shot :pluck :note (+ 40 (mod i 24))}]) (range 40)))`); err != nil {
		t.Errorf("a 40-step render script was refused: %v", err)
	}
}

// A string is Indexed in let-go but is not a vector, and a seq is refused
// rather than counted (counting (range) never returns).
func TestStringsAndSeqsAreNotVectors(t *testing.T) {
	c := renderLg(t, "notvectors")
	for _, src := range []string{`"abc"`, `(list 1 2)`, `(range)`, `(map inc [1 2])`} {
		v, err := c.Run(src)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := asVec(v); ok {
			t.Errorf("%s read as a vector", src)
		}
	}
	if v, _ := c.Run(`(first {:out 0.5})`); func() bool { _, ok := asVec(v); return !ok }() {
		t.Error("a map entry was not read as a vector")
	}
}
