package webaudio

import (
	"encoding/binary"
	"io"
	"math"
	"sync/atomic"
)

// Volume scales a 16-bit stereo source by a listening volume, for a page's
// volume control. It changes only what is heard: a meter that reads the
// source before it still reads the music at full level. Safe to Set from any
// goroutine while the player reads.
type Volume struct {
	src io.Reader
	v   atomic.Uint64 // float64 bits
}

// NewVolume wraps src at full volume.
func NewVolume(src io.Reader) *Volume {
	v := &Volume{src: src}
	v.v.Store(math.Float64bits(1))
	return v
}

// Set sets the volume, clamped to 0..1.
func (v *Volume) Set(x float64) {
	if !(x >= 0) { // NaN too
		x = 0
	}
	v.v.Store(math.Float64bits(min(x, 1)))
}

func (v *Volume) Read(p []byte) (int, error) {
	n, err := v.src.Read(p)
	if g := math.Float64frombits(v.v.Load()); g < 1 {
		for i := 0; i+1 < n; i += 2 {
			s := float64(int16(binary.LittleEndian.Uint16(p[i:])))
			binary.LittleEndian.PutUint16(p[i:], uint16(int16(math.Round(s*g))))
		}
	}
	return n, err
}
