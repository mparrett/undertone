# Contributing

## Before opening a PR

Run `just ready`. It runs every check below; each is also a recipe of its own.

| Recipe | What it covers | CI runs it |
|---|---|---|
| `just test` | the whole suite natively, the oto build of `letgo`, and the browser seam test under wasm | yes |
| `just race` | the tests that share a synth between goroutines, under the race detector | yes |
| `just lint` | `go vet` and staticcheck, native and for the wasm packages | all but `examples/studio` |
| `just test-wasm` | the `engine` and `letgo` suites as js/wasm under node, with `-short` | no |

`just grid-test` is not part of `ready`: it works the grid page
(`examples/grid`) in a headless browser, muted, and CI runs it as its `page`
job. Run it after changing that page or its Go bridge. It needs `uv`, and a
browser installed once with `uvx playwright@1.65.2 install chromium`.

`just test-wasm` needs Node on `PATH` (it took under two minutes as of
2026-11-19). CI leaves it out to save time, so running it is the only way a
change gets tested the way a browser runs it. That matters because Go on wasm has one thread and switches
goroutines only when one blocks. A test, or real code, that spins waiting for
another goroutine never finishes there, even though the race detector passes
natively.

When a test is too slow for wasm, trim its matrix under `testing.Short()`
rather than skipping it on js. Skip on js only what js cannot do at all, such
as `os.Pipe`.
