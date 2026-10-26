// Package lg embeds the demo's lg sources so the browser front end (GOOS=js,
// no filesystem) can load them through core's load-string. ambience.lg is not
// here: it ships with undertone's letgo package, which registers it as
// undertone.ambience.
package lg

import _ "embed"

var (
	//go:embed music.lg
	music string
	//go:embed sfx.lg
	sfx string
	//go:embed demo.lg
	demo string
)

// Source is one embedded namespace: its name and its text.
type Source struct {
	Name string
	Text string
}

// Sources lists the namespaces in dependency order: each must be registered
// before a namespace that requires it is loaded.
var Sources = []Source{
	{"music", music},
	{"sfx", sfx},
	{"demo", demo},
}
