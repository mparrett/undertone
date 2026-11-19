package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// The fields of a Slab project this tool reads, per Slab's published
// project-format doc (docs/19). Everything else in the file is ignored.
type project struct {
	Slab      string `json:"slab"`
	Transport struct {
		BPM float64 `json:"bpm"`
	} `json:"transport"`
	Meter  []meterPoint `json:"meter"`
	Tracks []track      `json:"tracks"`
}

type meterPoint struct {
	Bar int `json:"bar"`
	Num int `json:"num"`
	Den int `json:"den"`
}

type track struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Instrument struct {
		Machine string `json:"machine"`
	} `json:"instrument"`
	Clips []clip `json:"clips"`
	Time  *struct {
		Ratio []int `json:"ratio"`
	} `json:"time"`
}

type clip struct {
	Type  string  `json:"type"`
	Start float64 `json:"start"`
	Len   float64 `json:"len"`
	Muted bool    `json:"muted"`
	Notes []struct {
		Pitch int     `json:"pitch"`
		Start float64 `json:"start"`
		Len   float64 `json:"len"`
		Vel   int     `json:"vel"`
	} `json:"notes"`
}

// event is a note on the song timeline, in beats.
type event struct {
	beat, len float64
	pitch     int
	vel       int
	track     string
}

// loadProject reads a .slab package (a folder holding project.json) or a
// bare .slab file.
func loadProject(path string) (*project, error) {
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		path = filepath.Join(path, "project.json")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p project
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if p.Slab != "" && p.Slab != "project" {
		return nil, fmt.Errorf("%s: a Slab %q file, not a project", path, p.Slab)
	}
	return &p, nil
}

// findTrack looks a note track up by name, ignoring case. Buses share names
// with tracks in real projects (a DRUMS bus over a DRUMS track), and carry no
// notes, so they are skipped.
func (p *project) findTrack(name string) (*track, error) {
	var names []string
	for i := range p.Tracks {
		t := &p.Tracks[i]
		if t.Kind == "bus" {
			continue
		}
		if strings.EqualFold(t.Name, name) {
			return t, nil
		}
		names = append(names, fmt.Sprintf("%q", t.Name))
	}
	return nil, fmt.Errorf("no track %q; tracks: %s", name, strings.Join(names, ", "))
}

// events flattens a track's playing notes onto the song timeline, as Slab
// plays them: notes outside their clip's [0, len) and muted clips are silent,
// and a note is cut where its clip ends.
func (t *track) events() ([]event, error) {
	if t.Time != nil && len(t.Time.Ratio) == 2 && t.Time.Ratio[0] != t.Time.Ratio[1] {
		return nil, fmt.Errorf("track %q runs at its own tempo ratio %d:%d, which this tool does not convert", t.Name, t.Time.Ratio[0], t.Time.Ratio[1])
	}
	var evs []event
	for _, c := range t.Clips {
		if c.Muted || (c.Type != "" && c.Type != "note") {
			continue
		}
		for _, n := range c.Notes {
			if n.Start < 0 || n.Start >= c.Len {
				continue
			}
			l := math.Min(n.Len, c.Len-n.Start)
			evs = append(evs, event{c.Start + n.Start, l, n.Pitch, n.Vel, t.Name})
		}
	}
	return evs, nil
}

// barBeat names a song beat as Slab's bars count it, "bar:beat", both from 1,
// with the meter map's changes.
func (p *project) barBeat(beat float64) string {
	meter := p.Meter
	if len(meter) == 0 {
		meter = []meterPoint{{0, 4, 4}}
	}
	bar, at := 0, 0.0 // the bar and beat the current meter point starts on
	for i, m := range meter {
		per := barLen(m)
		if i+1 < len(meter) {
			end := at + float64(meter[i+1].Bar-m.Bar)*per
			if beat >= end {
				bar, at = meter[i+1].Bar, end
				continue
			}
		}
		k := math.Floor((beat - at) / per)
		return fmt.Sprintf("%d:%s", bar+int(k)+1, trimFloat(beat-at-k*per+1))
	}
	return trimFloat(beat)
}

// barLen is a bar's length in quarter-note beats.
func barLen(m meterPoint) float64 {
	if m.Num <= 0 || m.Den <= 0 {
		return 4
	}
	return float64(m.Num) * 4 / float64(m.Den)
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.3f", f)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
