// undertone webaudio, page side of worker mode. A worker playing through
// webaudio.Open renders into a shared ring and posts it as "undertone:open";
// this script creates the AudioContext and the worklet that plays the ring,
// and starts it on the first gesture (autoplay policy, and the iOS unlock that
// webaudio.go's unlockOnGesture describes).
//
// The worker usually belongs to someone else's runtime (let-go's wasm host
// creates its own), so this script can't take its onmessage. It wraps the
// Worker constructor instead and adds a listener to every worker the page
// creates, so it must load before the runtime's script.
(() => {
  // Every open player, for the gesture unlock. Each worker also keeps its own
  // id -> player map, so one player's messages never reach another's context,
  // whether the other lives in a second worker or in the same one.
  const all = new Set(); // of { ctx, node, held }

  const close = (c) => {
    if (c.node) c.node.disconnect();
    c.ctx.close();
    all.delete(c);
  };

  const unlock = () => {
    for (const c of all) {
      if (c.held || c.ctx.state === 'running') continue;
      const ctx = c.ctx;
      ctx.resume();
      const s = ctx.createBufferSource();
      s.buffer = ctx.createBuffer(1, 1, ctx.sampleRate);
      s.connect(ctx.destination);
      s.start(0);
    }
  };
  for (const ev of ['touchend', 'mouseup', 'keyup', 'click']) {
    document.addEventListener(ev, unlock, true);
  }

  const open = async (players, m) => {
    const AC = window.AudioContext || window.webkitAudioContext;
    const c = { ctx: new AC({ sampleRate: m.sampleRate }), node: null, held: false };
    players.set(m.id, c);
    all.add(c);
    const url = URL.createObjectURL(new Blob([m.processor], { type: 'application/javascript' }));
    await c.ctx.audioWorklet.addModule(url);
    if (players.get(m.id) !== c) return; // closed while the module loaded
    c.node = new AudioWorkletNode(c.ctx, 'undertone-shared', {
      outputChannelCount: [2],
      processorOptions: { cap: m.cap, data: m.data, ctrl: m.ctrl },
    });
    c.node.connect(c.ctx.destination);
    // Succeeds outright where autoplay is allowed. A Pause that arrived while
    // the module loaded stands: Open in the worker has already returned.
    if (!c.held) c.ctx.resume();
  };

  const listen = (worker) => {
    const players = new Map(); // player id -> { ctx, node, held }
    worker.addEventListener('message', (e) => {
      const m = e.data;
      if (!m || typeof m.t !== 'string' || !m.t.startsWith('undertone:')) return;
      if (m.t === 'undertone:open') {
        open(players, m).catch((err) => console.error('undertone:', err));
        return;
      }
      const c = players.get(m.id);
      if (!c) return;
      switch (m.t) {
        case 'undertone:pause': c.held = true; c.ctx.suspend(); break;
        case 'undertone:resume': c.held = false; c.ctx.resume(); break;
        case 'undertone:close': players.delete(m.id); close(c); break;
      }
    });
  };

  const Base = window.Worker;
  window.Worker = class extends Base {
    constructor(...args) {
      super(...args);
      listen(this);
    }
  };
})();
