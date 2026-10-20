//go:build !js

package nativeaudio

import (
	"testing"
	"time"
)

// oto turns our device buffer duration back into bytes by truncating integer
// arithmetic, then hands bytes/4/channelCount to createScriptProcessor on
// browsers without AudioWorklet — an API that throws unless that number is a
// power of two. So the round trip, not just the intent, has to land on one.
// This reproduces oto's own arithmetic (context.go / driver_js.go).
func TestDeviceBufferSurvivesOtosRoundTripAsAPowerOfTwo(t *testing.T) {
	for _, rate := range []int{44100, 48000} {
		deviceRoundTrip(t, rate)
	}
}

func deviceRoundTrip(t *testing.T, rate int) {
	const channelCount = 2
	bytesPerSecond := rate * bytesPerFrame

	b := int(int64(deviceBuffer(rate)) * int64(bytesPerSecond) / int64(time.Second))
	b = b / bytesPerFrame * bytesPerFrame // oto's alignment step
	frames := b / 4 / channelCount

	if frames&(frames-1) != 0 || frames == 0 {
		t.Errorf("at %d Hz the device buffer round-trips to %d frames, which is not a power of two; "+
			"ScriptProcessorNode would throw IndexSizeError and audio would die on "+
			"browsers without AudioWorklet", rate, frames)
	}
	if int64(frames) != deviceFrames {
		t.Errorf("at %d Hz the device buffer round-trips to %d frames, want %d", rate, frames, deviceFrames)
	}
}

func TestPlayerBufferIsWellFormedAndSmallerThanOtosDefault(t *testing.T) {
	const rate = 44100
	got := playerBufferBytes(rate, playerBuffer)
	if got%bytesPerFrame != 0 {
		t.Errorf("player buffer %d bytes is not a whole number of frames", got)
	}
	// oto's default is half a second, which is the latency this exists to cut.
	otoDefault := rate * bytesPerFrame / 2
	if got >= otoDefault {
		t.Errorf("player buffer %d >= oto's default %d — the point is to be smaller", got, otoDefault)
	}
	if playerBuffer < deviceBuffer(rate) {
		t.Errorf("player buffer (%v) below device buffer (%v): the device drains in whole "+
			"buffers, so read-ahead shorter than one cannot help", playerBuffer, deviceBuffer(rate))
	}
}
