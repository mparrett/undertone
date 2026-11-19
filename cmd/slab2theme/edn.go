package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/mparrett/undertone/engine"
)

// writeEDN writes a theme in the map undertone/play-theme takes:
// {:grids [{:lead [...] :bass [...] :pulse [...] :drums [...] :bed b} ...]
// :then :generate}. Lanes no track fed are left out, which the binding reads
// as rests. header lines go first as ; comments.
func writeEDN(w io.Writer, grids []engine.Grid, then string, has lanes, header []string) error {
	var b strings.Builder
	for _, h := range header {
		fmt.Fprintf(&b, ";; %s\n", h)
	}
	b.WriteString("{:grids\n [")
	for i, g := range grids {
		if i > 0 {
			b.WriteString("\n  ")
		}
		b.WriteString("{")
		var fields []string
		if len(has.lead) > 0 {
			fields = append(fields, ":lead  "+ints(g.Lead[:]))
		}
		if len(has.bass) > 0 {
			fields = append(fields, ":bass  "+ints(g.Bass[:]))
		}
		if len(has.pulse) > 0 {
			fields = append(fields, ":pulse "+bools(g.Pulse[:]))
		}
		if len(has.drums) > 0 {
			q := make([]string, len(g.Drums))
			for d, lane := range g.Drums {
				q[d] = fmt.Sprintf("%q", lane)
			}
			fields = append(fields, ":drums ["+strings.Join(q, "\n           ")+"]")
		}
		fields = append(fields, fmt.Sprintf(":bed %t", g.Bed))
		b.WriteString(strings.Join(fields, "\n   "))
		b.WriteString("}")
	}
	fmt.Fprintf(&b, "]\n :then :%s}\n", then)
	_, err := io.WriteString(w, b.String())
	return err
}

func ints(xs []int) string {
	s := make([]string, len(xs))
	for i, x := range xs {
		s[i] = fmt.Sprint(x)
	}
	return "[" + strings.Join(s, " ") + "]"
}

func bools(xs []bool) string {
	s := make([]string, len(xs))
	for i, x := range xs {
		s[i] = fmt.Sprint(x)
	}
	return "[" + strings.Join(s, " ") + "]"
}
