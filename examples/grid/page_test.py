#!/usr/bin/env python3
# /// script
# requires-python = ">=3.10"
# dependencies = ["playwright==1.65.2"]
# ///
"""The grid page, driven in a real browser.

Builds the page into a temporary folder, serves it on localhost, and works it
in headless Chromium as a hand would: slots, the chain, follow, undo, the
ghost of the next steps, looping drum lanes. It makes no sound (the browser is
muted) but it does play: several checks wait on the playhead, so the run is
as long as the patterns they wait for. It prints its time at the end.

    uv run examples/grid/page_test.py           # or: just grid-test
    uvx playwright@1.65.2 install chromium      # once, for the browser

The page keeps its state in script-level variables (st, slot, patRun, ...),
and the checks read those where the screen does not say enough. A check that
breaks on a rename is this file's to fix, not the page's to avoid.
"""
import functools
import http.server
import json
import os
import shutil
import subprocess
import sys
import tempfile
import threading
import time

from playwright.sync_api import Error as BrowserError
from playwright.sync_api import TimeoutError as BrowserTimeout
from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
REST = "." * 32
FAST = 160  # a pattern is three seconds at this tempo

failures = []


def check(name, cond, detail=""):
    print(("ok    " if cond else "FAIL  ") + name, "" if cond else detail, flush=True)
    if not cond:
        failures.append(name)


def build(site):
    """The page as the pages workflow lays it out: index.html, main.wasm, wasm_exec.js."""
    env = dict(os.environ, GOOS="js", GOARCH="wasm")
    subprocess.run(["go", "build", "-o", os.path.join(site, "main.wasm"), "./examples/grid"], cwd=REPO, env=env, check=True)
    goroot = subprocess.run(["go", "env", "GOROOT"], cwd=REPO, capture_output=True, text=True, check=True).stdout.strip()
    shutil.copy(os.path.join(goroot, "lib", "wasm", "wasm_exec.js"), site)
    shutil.copy(os.path.join(HERE, "index.html"), site)


def serve(site):
    class Quiet(http.server.SimpleHTTPRequestHandler):
        def log_message(self, *a):
            pass

    srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), functools.partial(Quiet, directory=site))
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv, f"http://127.0.0.1:{srv.server_address[1]}/"


def link(tempo, lead, kick=REST):
    return f"#v1;t={tempo};m=overworld;v=kalimba;s=1;bed=1;L={lead};B=0000000000000000;P=1;D={kick},{REST},{REST}"


class Page:
    """A fresh browser profile on the page, with the given slots kept in it."""

    def __init__(self, browser, url, slots, chain=0, width=1440, height=900):
        self.ctx = browser.new_context(viewport={"width": width, "height": height})
        self.pg = self.ctx.new_page()
        self.errors = []
        self.pg.on("pageerror", lambda e: self.errors.append(str(e)))
        # Before the page's own script, and once: seeded after a first load, the page could keep
        # its opening pattern over slot 1 on its way out.
        self.ctx.add_init_script(
            "if (!localStorage.getItem('seeded')) { localStorage.setItem('seeded', '1'); localStorage.setItem('undertone-grid-slots', %s);"
            " localStorage.setItem('undertone-grid-slot', '0'); localStorage.setItem('undertone-grid-chain', '%d'); }"
            % (json.dumps(json.dumps([{"link": s} for s in slots])), chain)
        )
        self.pg.goto(url)
        self.pg.wait_for_function("document.getElementById('play').disabled === false", timeout=60000)

    def __enter__(self):
        return self

    def __exit__(self, *a):
        self.ctx.close()

    def js(self, expr):
        return self.pg.evaluate(expr)

    def wait(self, expr, timeout=20):
        self.pg.wait_for_function(expr, timeout=timeout * 1000)

    def reaches(self, expr, timeout=20):
        """wait, for a check of its own: False where wait would end the group."""
        try:
            self.wait(expr, timeout)
            return True
        except BrowserTimeout:
            return False

    def settle(self):
        """Past the 600 ms in which changes count as one undo step."""
        time.sleep(0.8)

    def key(self, k):
        self.pg.keyboard.press(k)
        time.sleep(0.2)

    def click_cell(self, lane, deg, step):
        self.pg.locator(f"#{lane} .c[data-deg='{deg}'][data-step='{step}']").click()

    def chain(self, label):
        self.pg.click(".pick.chain .pickbtn")
        self.pg.locator("#chain .chip", has_text=label).click()
        self.pg.keyboard.press("Escape")
        time.sleep(0.2)

    def tempo(self, bpm):
        self.js(f"(() => {{ const f = document.getElementById('bpm'); f.value = {bpm}; f.dispatchEvent(new Event('input')); f.dispatchEvent(new Event('change')); }})()")
        self.settle()

    def play(self):
        self.pg.click("#play")

    def links(self):
        return self.js("JSON.parse(localStorage.getItem('undertone-grid-slots')).map((s) => s && s.link)")

    def lead(self):
        return self.js("st.lead.join('')")

    def undo_off(self):
        return self.js("document.getElementById('undo').disabled")


# the kick lane of the ghost, and of the grid: x where a cell is drawn on
GHOST_KICK = "[...document.querySelectorAll('#drums .ghost .g')].filter((c) => c.dataset.r === '0').map((c) => c.classList.contains('on') ? 'x' : '.').join('')"
GRID_KICK = "[...document.querySelectorAll('#drums .c[data-drum=\"0\"]')].map((c) => c.classList.contains('on') ? 'x' : '.').join('')"


def looping_lanes(browser, url):
    """A lane that loops at its own length is drawn where the engine plays it, pass after pass."""
    loop = "x.."
    with Page(browser, url, [link(FAST, "1000000000000000", loop + "|"), link(FAST, "0200000000000000", "x....|")]) as p:
        check("stopped, the ghost shows a loop of 3 running on", p.js(GHOST_KICK) == ".x..x..x", p.js(GHOST_KICK))
        p.play()
        # the cells against the engine's own rule, from the run it reports with the playhead
        offsets, wrong, samples = set(), 0, 0
        t0 = time.time()
        while time.time() - t0 < 7.5:
            row, _, run = p.js("undertoneGrid.at()")
            drawn = p.js(GRID_KICK)
            if row >= 0:
                at = (2 * (run - row)) % len(loop)
                want = "".join(loop[(at + i) % len(loop)] for i in range(32))
                samples += 1
                wrong += drawn != want
                offsets.add(at)
            time.sleep(0.05)
        # a sample may straddle the turn of a pattern: read just before it, drawn just after
        check("playing, the looping lane is drawn by the run", samples > 50 and wrong <= 2, f"{wrong} of {samples} samples differ")
        check("and its place moves from pass to pass", len(offsets) >= 2, offsets)
        p.play()
        p.key("r")
        check("stopped and rewound, the run starts over", p.js("patRun") == 0, p.js("patRun"))
        p.chain("1–2")
        check("in a chain, the ghost shows the next slot's loop of 5", p.js(GHOST_KICK) == "...x....", p.js(GHOST_KICK))

        # A chain unlinked while it plays hands back to the grid mid-pattern, so the run begins
        # partway through one and the pattern's start is before it.
        p.play()
        # early enough that the clicks land before the engine has rendered into the next pattern
        p.wait("(() => { const r = undertoneGrid.at()[0]; return r >= 4 && r <= 7; })()")
        p.chain("off")
        low, whole, t0 = 0, True, time.time()
        while time.time() - t0 < 2.5:
            run, ok = p.js("[patRun, !!st.drums[0].loop && [...Array(32)].every((_, i) => 'Xxo.?f'.includes(shown(0, i)))]")
            low, whole = min(low, run), whole and ok
            time.sleep(0.05)
        check("unlinked mid-pattern, the run begins partway through it", low < 0, low)
        check("and the looping lane is still drawn whole", whole)
        p.play()
        check("no page errors (looping lanes)", not p.errors, p.errors)


def chain_settings(browser, url):
    """A chain's settings are written to each of its slots; a slot outside it keeps its own."""
    with Page(browser, url, [link(90, "1000000000000000"), link(140, "0200000000000000"), link(120, "0030000000000000")]) as p:
        p.chain("1–2")
        ls = p.links()
        check("chaining gives slot 2 the tempo on screen and keeps its notes", "t=90;" in ls[1] and "L=0200000000000000" in ls[1], ls[1])
        check("slot 3, outside the chain, keeps its own", "t=120;" in ls[2], ls[2])
        p.tempo(FAST)
        ls = p.links()
        check("a tempo change reaches both slots", all(f"t={FAST};" in s for s in ls[:2]), ls[:2])
        check("no page errors (chain settings)", not p.errors, p.errors)


def undo_per_slot(browser, url):
    """Each slot has its own undo, and in a chain it does not take back a setting changed from another."""
    one, two = "1000000000000000", "0200000000000000"
    with Page(browser, url, [link(FAST, one), link(FAST, two)], chain=2) as p:
        p.pg.click("#follow")  # off: the grid stays where it is put
        p.click_cell("lead", 5, 4)
        p.settle()
        p.key("2")
        check("slot 2 arrives with nothing to undo", p.undo_off())
        p.click_cell("lead", 6, 8)
        p.settle()
        p.key("1")
        check("back on slot 1 its step is still there", not p.undo_off() and p.lead() != one, p.lead())
        p.pg.click("#undo")
        check("undo takes slot 1's note back", p.lead() == one, p.lead())
        check("and leaves slot 2's", "L=020000006" in p.links()[1], p.links()[1])
        p.key("2")
        p.pg.click("#undo")
        check("undo on slot 2 takes its own back", p.lead() == two, p.lead())

        p.key("1")
        p.click_cell("lead", 5, 4)
        p.settle()
        p.key("2")
        p.tempo(100)
        p.key("1")
        p.pg.click("#undo")
        got = p.js("[st.lead.join(''), st.bpm]")
        check("undo on slot 1 leaves a tempo set from slot 2", got == [one, 100], got)
        check("with nothing more to undo there", p.undo_off())
        check("no page errors (undo)", not p.errors, p.errors)


def follow(browser, url):
    """Follow shows the slot playing, and waits while the one on screen is being edited."""
    with Page(browser, url, [link(FAST, "1000000000000000"), link(FAST, "0200000000000000")], chain=2) as p:
        p.play()
        p.wait("slot === 1 && heardSlot === 1")
        check("the grid moves to slot 2 as it starts", p.lead() == "0200000000000000", p.lead())
        p.wait("slot === 0 && heardSlot === 0")

        # a hand at work: a cell every quarter second for more than two patterns
        seen, held, k, t0 = set(), False, 0, time.time()
        while time.time() - t0 < 7:
            p.click_cell("bass", 3, k % 16)
            k += 1
            sl, h = p.js("[slot, document.getElementById('follow').classList.contains('held')]")
            seen.add(sl)
            held = held or h
            time.sleep(0.25)
        check("the grid stays on slot 1 while it is edited", seen == {0}, seen)
        check("and follow shows that it is waiting", held)
        check("it moves on once the hand stops", p.reaches("slot === 1", timeout=10))

        # typing in the pattern box is an edit too: applied after the turn, it would land in the next slot
        p.wait("slot === 0 && heardSlot === 0")
        p.js("(() => { showEDNBox(true); document.getElementById('patedn').focus(); })()")
        seen, t0 = set(), time.time()
        while time.time() - t0 < 4.5:
            seen.add(p.js("slot"))
            time.sleep(0.1)
        check("the grid stays while the pattern box has the cursor", seen == {0}, seen)
        p.js("(() => { document.activeElement.blur(); showEDNBox(false); })()")

        p.key("1")
        check("picking a slot by hand turns follow off", p.js("follow") is False)
        p.play()
        check("no page errors (follow)", not p.errors, p.errors)


def layout(browser, url):
    """The ghost goes inside the grid's column, and only where there is room for it."""
    slots = [link(110, "1000000000000000")]
    width = "Math.round(document.querySelector('#lead .c').getBoundingClientRect().width)"
    scroll = "document.documentElement.scrollWidth <= innerWidth"
    with Page(browser, url, slots) as p:
        col = p.js("(() => { const g = document.getElementById('lead').getBoundingClientRect(), h = document.querySelector('#lead .ghost').getBoundingClientRect(); return Math.round(h.right - g.left); })()")
        check("next on: the grid and the ghost fill the 16-step column", 930 <= col <= 946, col)
        p.pg.click("#nextbtn")
        check("next off: a cell is 56px at this width", p.js(width) == 56, p.js(width))
        check("no sideways scroll at 1440", p.js(scroll))
        check("no page errors (layout)", not p.errors, p.errors)
    with Page(browser, url, slots, width=390, height=844) as p:
        check("on a phone the ghost is not shown", p.js("getComputedStyle(document.querySelector('.ghost')).display") == "none")
        check("no sideways scroll on a phone", p.js(scroll))
        check("no page errors (phone)", not p.errors, p.errors)


def main():
    began = time.time()
    site = tempfile.mkdtemp(prefix="undertone-grid-")
    try:
        build(site)
        srv, url = serve(site)
        with sync_playwright() as pw:
            # muted, and allowed to start audio without a tap: the playhead follows the audio clock
            browser = pw.chromium.launch(args=["--autoplay-policy=no-user-gesture-required", "--mute-audio"])
            for group in (looping_lanes, chain_settings, undo_per_slot, follow, layout):
                # a wait that times out ends its group, not the run: the rest still report
                try:
                    group(browser, url)
                except BrowserError as e:
                    check(f"{group.__name__} ran to its end", False, str(e).splitlines()[0])
            browser.close()
        srv.shutdown()
    finally:
        shutil.rmtree(site, ignore_errors=True)
    took = f"in {time.time() - began:.0f}s"
    if failures:
        print(f"\n{len(failures)} failed {took}: " + "; ".join(failures))
        sys.exit(1)
    print(f"\nall passed {took}")


if __name__ == "__main__":
    main()
