//go:build !js

package main

import (
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/mparrett/undertone/engine"
)

// midiSink sends channel messages now. The live loop times them itself: a
// receiver may ignore CoreMIDI timestamps and play a message when it
// arrives, so a message stamped for later would play early.
type midiSink interface {
	send(data [3]byte) error
	close()
}

// liveAhead is how far the synth renders ahead of the clock, so a note's
// length is known (the next note on its track cuts it) before its off is
// due.
const liveAhead = 60 * time.Millisecond

// playMIDILive renders s in real time, without audio, and sends its notes
// to a virtual MIDI source until interrupted.
func playMIDILive(s *engine.Synth, p engine.Params, desc string) error {
	out, err := openVirtualSource("undertone")
	if err != nil {
		return err
	}
	defer out.close()
	fmt.Printf("playing %s to MIDI source \"undertone\" — ctrl-c to stop\n", desc)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	t := &noteTap{s: s, m: newNoteMapper(p)}
	buf := make([]byte, tapBlock)
	ahead := int64(liveAhead.Seconds() * engine.SampleRate)
	start := time.Now()
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	warned := false
	for {
		select {
		case <-sig:
			fmt.Println()
			return allOff(out, t.m)
		case <-tick.C:
		}
		now := int64(time.Since(start).Seconds() * engine.SampleRate)
		for s.Frames() < now+ahead {
			t.Read(buf)
		}
		if t.err != nil && !warned {
			fmt.Fprintln(os.Stderr, t.err)
			warned = true
		}
		n := 0
		for _, m := range t.m.out {
			if m.frame > now {
				break
			}
			if err := out.send(m.data); err != nil {
				return err
			}
			n++
		}
		t.m.out = append(t.m.out[:0], t.m.out[n:]...)
	}
}

// allOff ends the notes still sounding and sends all-notes-off on each
// channel this tool uses, so nothing hangs in the receiver.
func allOff(out midiSink, m *noteMapper) error {
	for i := range m.held {
		m.release(i, 0)
	}
	for _, msg := range m.out {
		if msg.data[0]&0xf0 == 0x80 {
			out.send(msg.data)
		}
	}
	for ch := byte(0); ch < engine.NumLayers-1; ch++ {
		out.send([3]byte{0xb0 | ch, 123, 0})
	}
	return out.send([3]byte{0xb0 | drumChannel, 123, 0})
}
