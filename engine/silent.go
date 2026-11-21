package engine

import (
	"encoding/binary"
	"io"
	"math"
)

// SilentFloorDB is where a take rendered until silent ends: the last frame at
// or above -60 dBFS. The sfx board marks a sound's audible end at the same
// level; below it is only the faint end of echoes and room.
const SilentFloorDB = -60.0

// silentMarginMS is kept past the last audible frame, so the end of a take is
// the start of silence rather than the last loud sample.
const silentMarginMS = 10

// echoGapMS is added to the delay's period before the output counts as gone
// quiet: an echo returns one period after the sound that fed it, so a gap
// shorter than that is between echoes, not after the last.
const echoGapMS = 50

// shotsIdle reports whether the one-shot lane has nothing left to play: no
// trigger waiting for its time and no voice sounding. Call it only from the
// goroutine reading s; it reads the audio thread's state unguarded.
func (s *Synth) shotsIdle() bool {
	if s.fx.waiting.Load() || s.fx.fresh.Load() {
		return false
	}
	for i := range s.fx.shots {
		if s.fx.shots[i].active {
			return false
		}
	}
	return true
}

// ReadUntilSilent reads src, the output of s or of something reading through
// it (a reader that applies calls on the way), until the sound has ended: the
// one-shot lane is idle, more (nil for none) reports nothing still to come,
// and the output has stayed below floorDB for the delay's period plus
// echoGapMS. It returns the frames up to the last one at or above floorDB plus
// silentMarginMS, as interleaved stereo samples, reading at most capSeconds.
//
// The result is a prefix of what a fixed-length read of src gives, so a take
// rendered again for as many frames is identical to it. A bed playing above
// the floor never goes quiet, and its take runs to the cap.
func ReadUntilSilent(s *Synth, src io.Reader, capSeconds, floorDB float64, more func() bool) []int16 {
	capFrames := int(min(max(capSeconds, 0), MaxTakeSeconds) * SampleRate)
	thr := 32767 * math.Pow(10, floorDB/20)
	margin := silentMarginMS * SampleRate / 1000
	var out []int16
	var buf [1024 * 4]byte
	last := -1 // the last frame at or above the floor
	for n := 0; n < capFrames; {
		b := buf[:4*min(len(buf)/4, capFrames-n)]
		if _, err := io.ReadFull(src, b); err != nil {
			break
		}
		for k := 0; k < len(b); k, n = k+4, n+1 {
			l := int16(binary.LittleEndian.Uint16(b[k:]))
			r := int16(binary.LittleEndian.Uint16(b[k+2:]))
			out = append(out, l, r)
			if math.Abs(float64(l)) >= thr || math.Abs(float64(r)) >= thr {
				last = n
			}
		}
		hold := len(s.dl) + echoGapMS*SampleRate/1000
		if n-(last+1) >= max(hold, margin) && s.shotsIdle() && (more == nil || !more()) {
			break
		}
	}
	return out[:min(len(out), 2*(last+1+margin))]
}

// RenderUntilSilent renders the take as Render does, but for as long as it
// sounds (see ReadUntilSilent) instead of for Seconds, which becomes the cap;
// a Seconds of 0 caps it at MaxTakeSeconds.
func (t Take) RenderUntilSilent(floorDB float64) []int16 {
	s := NewSynth(t.Params)
	s.SetMusicGain(t.MusicGain)
	for _, sh := range t.Shots {
		s.TriggerShot(sh)
	}
	capSeconds := t.Seconds
	if capSeconds <= 0 {
		capSeconds = MaxTakeSeconds
	}
	return ReadUntilSilent(s, s, capSeconds, floorDB, nil)
}

// UntilSilentHash is Hash for the take rendered until silent at floorDB: the
// same digest of what decides the samples, plus the floor that decides where
// they end.
func (t Take) UntilSilentHash(floorDB float64) string {
	return t.hash("until-silent " + formatFloat(floorDB))
}
