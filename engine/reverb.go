package engine

import "math"

// sends are the buses a voice feeds besides its own output: the delay and
// the reverb, each mixed back in after the voices.
type sends struct{ delay, verb float64 }

// Room is a mood's reverb room, as offsets from the default: the zero Room
// is the long, dark room every mood had before rooms were a choice. Both
// fields run -1..1 and are clamped to it.
//
// Size sets the combs' feedback, and so how long the tail rings: -1 is
// Freeverb's smallest room (feedback 0.7), 0 the default (0.94), 1 its
// largest (0.98), linear on each side of 0. Damp sets the share of each
// comb's output lowpassed away per pass, and so how fast the highs die
// before the lows: -1 none (a bright room), 0 the default 0.3, 1 twice that.
type Room struct{ Size, Damp float64 }

// reverb is a Freeverb-style network, after Jezar's public-domain design:
// per channel, eight damped feedback combs in parallel, then four allpasses in
// series, the right channel's lines a little longer for width. The tunings are
// Freeverb's, for 44.1 kHz.
type reverb struct {
	comb [2][8]combLine
	ap   [2][4]allpass
	on   bool // set by a send; off again once the room has been quiet a while
	idle int  // samples since the last send

	// The room's coefficients glide to their targets, so a change mid-tail
	// bends the decay rather than stepping the tail's level a comb later.
	// keep is 1-damp, set apart so the default room's is the exact constant.
	fb, damp, keep       float64
	fbTo, dampTo, keepTo float64
	idleAfter            int // verbIdle, rescaled to the room's decay
}

// verbIdle is how long after its last send the default room turns off: past
// the tail's fall below -100 dB (TestReverbTailDecays), so nothing audible is
// cut.
const verbIdle = 12 * SampleRate

type combLine struct {
	buf   []float64
	pos   int
	store float64 // the damping lowpass
}

type allpass struct {
	buf []float64
	pos int
}

var (
	combTuning = [8]int{1116, 1188, 1277, 1356, 1422, 1491, 1557, 1617}
	apTuning   = [4]int{556, 441, 341, 225}
)

const (
	verbSpread   = 23    // samples added to each right-channel line
	verbIn       = 0.015 // Freeverb's fixed input gain
	verbFeedback = 0.94  // comb feedback: a long, dark room (Freeverb room 0.86)
	verbDamp     = 0.3   // share of each comb's output lowpassed away per pass
	verbWet      = 0.45  // sets the tail against the dry: about 6 dB under a sustained bed
)

func newReverb() *reverb {
	v := &reverb{}
	v.setRoom(Room{})
	for ch := range 2 {
		for i, n := range combTuning {
			v.comb[ch][i].buf = make([]float64, n+ch*verbSpread)
		}
		for i, n := range apTuning {
			v.ap[ch][i].buf = make([]float64, n+ch*verbSpread)
		}
	}
	return v
}

// setRoom sets the room the reverb glides to; a room that is not ringing
// takes it at once. r must be sanitized: feedback stays below 1 only for a
// Size within -1..1.
func (v *reverb) setRoom(r Room) {
	span := verbFeedback - 0.7
	if r.Size > 0 {
		span = 0.98 - verbFeedback
	}
	v.fbTo = verbFeedback + span*r.Size
	v.dampTo = verbDamp + verbDamp*r.Damp
	v.keepTo = (1 - verbDamp) - verbDamp*r.Damp
	// The tail falls by a factor of fb each pass, so the time it takes to
	// fall below -100 dB scales with 1/ln(fb). Damping only shortens it.
	v.idleAfter = int(verbIdle * (math.Log(verbFeedback) / math.Log(v.fbTo)))
	if !v.on {
		v.fb, v.damp, v.keep = v.fbTo, v.dampTo, v.keepTo
	}
}

// verbGlide is the share of the way to a new room covered per sample: a
// time constant of about 46 ms.
const verbGlide = 1.0 / 2048

func (v *reverb) glideRoom() {
	v.fb += (v.fbTo - v.fb) * verbGlide
	v.damp += (v.dampTo - v.damp) * verbGlide
	v.keep += (v.keepTo - v.keep) * verbGlide
	if math.Abs(v.fbTo-v.fb) < 1e-5 && math.Abs(v.dampTo-v.damp) < 1e-5 {
		v.fb, v.damp, v.keep = v.fbTo, v.dampTo, v.keepTo
	}
}

// clear silences the room and turns it off, so what it renders next does not
// depend on how long ago it last rang.
func (v *reverb) clear() {
	for ch := range 2 {
		for i := range v.comb[ch] {
			c := &v.comb[ch][i]
			clear(c.buf)
			c.pos, c.store = 0, 0
		}
		for i := range v.ap[ch] {
			clear(v.ap[ch][i].buf)
			v.ap[ch][i].pos = 0
		}
	}
	v.on, v.idle = false, 0
}

// process takes one sample of the reverb bus and returns the wet stereo pair.
func (v *reverb) process(in float64) (float64, float64) {
	if in != 0 {
		v.on, v.idle = true, 0
	} else if v.on {
		if v.idle++; v.idle >= v.idleAfter {
			v.clear()
			return 0, 0
		}
	}
	if !v.on {
		return 0, 0
	}
	if v.fb != v.fbTo || v.damp != v.dampTo {
		v.glideRoom()
	}
	in *= verbIn
	var out [2]float64
	for ch := range 2 {
		sum := 0.0
		for i := range v.comb[ch] {
			c := &v.comb[ch][i]
			y := c.buf[c.pos]
			c.store = flush(y*v.keep + c.store*v.damp)
			c.buf[c.pos] = in + c.store*v.fb
			c.pos++
			if c.pos == len(c.buf) {
				c.pos = 0
			}
			sum += y
		}
		for i := range v.ap[ch] {
			a := &v.ap[ch][i]
			y := a.buf[a.pos]
			a.buf[a.pos] = flush(sum + y*0.5)
			sum = y - sum
			a.pos++
			if a.pos == len(a.buf) {
				a.pos = 0
			}
		}
		out[ch] = sum * verbWet
	}
	return out[0], out[1]
}
