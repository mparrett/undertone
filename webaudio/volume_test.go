package webaudio

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestVolumeScalesWhatIsHeard(t *testing.T) {
	in := make([]byte, 8)
	for i, s := range []int16{32767, -32768, 1000, -3} {
		binary.LittleEndian.PutUint16(in[2*i:], uint16(s))
	}
	read := func(x float64) []int16 {
		v := NewVolume(bytes.NewReader(in))
		v.Set(x)
		out := make([]byte, len(in))
		if n, _ := v.Read(out); n != len(in) {
			t.Fatalf("read %d of %d", n, len(in))
		}
		got := make([]int16, 4)
		for i := range got {
			got[i] = int16(binary.LittleEndian.Uint16(out[2*i:]))
		}
		return got
	}
	if got := read(1); got[0] != 32767 || got[1] != -32768 || got[3] != -3 {
		t.Errorf("full volume changed the samples: %v", got)
	}
	if got := read(0.25); got[0] != 8192 || got[1] != -8192 || got[2] != 250 || got[3] != -1 {
		t.Errorf("volume 0.25: %v, want [8192 -8192 250 -1]", got)
	}
	if got := read(0); got[0] != 0 || got[1] != 0 {
		t.Errorf("volume 0: %v, want silence", got)
	}
	if got := read(7); got[0] != 32767 {
		t.Errorf("volume past 1 is clamped to 1, got %v", got)
	}
}
