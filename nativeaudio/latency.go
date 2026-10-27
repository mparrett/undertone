//go:build !js

package nativeaudio

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Audio latency budget.
//
// oto's defaults are tuned for streaming music, where a deep buffer costs
// nothing: the device buffer is 2048 frames and the PLAYER reads a further
// half second ahead (mux.defaultBufferSize). For a bed that is correct and
// invisible. For one-shots it is not — a trigger staged now waits for all
// that already-queued audio to drain, so a footstep lands somewhere in the
// next half second. Before this, the tuning bench (headless Chromium through
// oto's web backend, 2026-08-24) measured a median of 821 ms from tap to
// first sample, ranging 33 ms to over a second depending on where in the
// buffer cycle the tap fell. The variance is the worse half; a late
// sound is survivable, a sound whose delay changes every time cannot be
// played around.
//
// These are the values Open uses for every native caller; browsers play
// through the webaudio package, not oto. The values are a trade-off, not a
// maximum: smaller buffers leave the renderer less slack before an underrun,
// which is audible as a click — and in the game the same main thread also
// draws the terminal. If dropouts appear, raise the device buffer first (in
// the power-of-two steps below), since it bounds how long the renderer may
// stall.
const (
	// deviceFrames is the device buffer in frames per channel, kept a power
	// of two. That was a hard rule while browsers played through oto: its
	// ScriptProcessorNode fallback passes exactly this number to
	// createScriptProcessor, which throws IndexSizeError for anything else.
	// Browsers now play through webaudio, but the round trip below still
	// holds it. On macOS oto splits it across four AudioQueue buffers.
	//
	// 1024 is what every build before 2026-10-26 actually got: deviceBuffer
	// then converted at 4 bytes a frame while oto converts at 8, so the 512
	// written here opened a 1024-frame device.
	deviceFrames int64 = 1024

	// playerBuffer is the player's read-ahead, and the knob that actually
	// moves latency. Native, measured 2026-10-26 on a MacBook Air (built-in
	// speakers into its own mic, trigger to the blip's onset in the
	// recording; medians of 15; frames are what the device actually got):
	//
	//	40 ms read-ahead, 1024 frames  -> 150-158 ms  (the defaults until now)
	//	24 ms read-ahead, 1024 frames  -> 119-128 ms  (these defaults)
	//	24 ms read-ahead,  512 frames  -> 117 ms
	//	12 ms read-ahead,  512 frames  ->  97 ms
	//	256 frames, at 6 or 12 ms      -> breaks up; no clean onsets
	//
	// Onset timing cannot hear an underrun: 512 frames gave clean onsets
	// above, yet a few minutes of tesserae at 24 ms / 512 crackled the whole
	// time ("sounds like gravel"; same MacBook Air, same day). On macOS oto
	// splits the device buffer across four AudioQueue buffers, so 512 frames
	// is four buffers of 2.9 ms. 24 ms / 1024 and 40 ms / 1024 both played
	// clean in that test.
	//
	// The earlier table here (a 90 ms floor plus 1.4x the read-ahead, and
	// 821 ms at oto's 500 ms default) was measured 2026-08-24 in headless
	// Chromium through oto's web backend, timed by Playwright: a browser
	// figure, not a native one.
	//
	// 24 ms is the shortest read-ahead the 1024-frame device allows at
	// 44.1 kHz (one buffer, 23.2 ms); overrides lengthens the default to a
	// whole buffer where a buffer is longer. At 44.1 kHz it leaves ~47 ms for the
	// renderer to stall before an underrun, on a main thread that in the
	// game is also drawing the terminal. Lower latency needs a smaller
	// device buffer, and 512 frames crackled. Re-check by ear for clicks
	// after any change here; no probe so far can hear them.
	playerBuffer = 24 * time.Millisecond
)

// deviceBuffer is deviceFrames expressed as the duration oto wants, rounded
// UP. oto converts back by truncating, so a duration that divides
// exactly-but-short lands a byte low and then aligns down to one frame less —
// turning 512 into 511 and losing the power of two. Rounding up costs a
// fraction of a millisecond and makes the round trip exact. (A flat slack
// term does not work: 1 ms is 44 frames too many.)
func deviceBuffer(rate int, frames int64) time.Duration {
	deviceBytes := frames * otoBytesPerFrame
	perSecond := int64(rate) * otoBytesPerFrame
	return time.Duration((deviceBytes*int64(time.Second) + perSecond - 1) / perSecond)
}

// otoBytesPerFrame is how oto converts NewContextOptions.BufferSize to bytes:
// its drivers always run in stereo float32, whatever format players use.
const otoBytesPerFrame = 2 * 4

// playerBufferBytes is a read-ahead of d as oto's SetBufferSize wants it:
// bytes, aligned to a whole frame (2 channels x 2 bytes for signed 16-bit LE).
func playerBufferBytes(rate int, d time.Duration) int {
	n := int(int64(d) * int64(rate) * bytesPerFrame / int64(time.Second))
	return n / bytesPerFrame * bytesPerFrame
}

const bytesPerFrame = 2 * 2 // stereo, signed 16-bit

// Two environment variables override the buffers for latency experiments,
// without a rebuild: UNDERTONE_PLAYER_BUFFER, a Go duration such as "20ms",
// replaces the read-ahead (and any Options.Queue); UNDERTONE_DEVICE_FRAMES
// replaces deviceFrames, and must be a power of two from 64 to 16384, the
// range createScriptProcessor accepts. The device buffer is fixed when the
// first player opens the shared context. A bad value is an error from Open,
// not a silent fallback, so an experiment never measures the default by
// mistake.
func overrides(rate int, queue time.Duration) (time.Duration, int64, error) {
	frames := deviceFrames
	if v := os.Getenv("UNDERTONE_PLAYER_BUFFER"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return 0, 0, fmt.Errorf("nativeaudio: UNDERTONE_PLAYER_BUFFER=%q is not a positive duration", v)
		}
		queue = d
	}
	if v := os.Getenv("UNDERTONE_DEVICE_FRAMES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 64 || n > 16384 || n&(n-1) != 0 {
			return 0, 0, fmt.Errorf("nativeaudio: UNDERTONE_DEVICE_FRAMES=%q is not a power of two from 64 to 16384", v)
		}
		frames = n
	}
	// With no read-ahead asked for, take playerBuffer, or one device buffer
	// where that is longer: at rates under ~42.7 kHz, or with more device
	// frames, 24 ms no longer covers a buffer, and the default must not be
	// an error.
	if queue == 0 {
		queue = max(playerBuffer, deviceBuffer(rate, frames))
	}
	// Shorter than one device buffer can't help (the device drains whole
	// buffers), and under one frame it rounds to 0 bytes, which oto reads as
	// "use the half-second default" — the silent fallback this rejects.
	if queue < deviceBuffer(rate, frames) {
		return 0, 0, fmt.Errorf("nativeaudio: a read-ahead of %v is shorter than the %d-frame device buffer (%v)",
			queue, frames, deviceBuffer(rate, frames))
	}
	return queue, frames, nil
}
