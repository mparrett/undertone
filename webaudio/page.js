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
  let cur = null; // { ctx, node, held }

  const close = () => {
    if (!cur) return;
    if (cur.node) cur.node.disconnect();
    cur.ctx.close();
    cur = null;
  };

  const unlock = () => {
    if (!cur || cur.held || cur.ctx.state === 'running') return;
    const ctx = cur.ctx;
    ctx.resume();
    const s = ctx.createBufferSource();
    s.buffer = ctx.createBuffer(1, 1, ctx.sampleRate);
    s.connect(ctx.destination);
    s.start(0);
  };
  for (const ev of ['touchend', 'mouseup', 'keyup', 'click']) {
    document.addEventListener(ev, unlock, true);
  }

  const open = async (m) => {
    close();
    const AC = window.AudioContext || window.webkitAudioContext;
    const c = cur = { ctx: new AC({ sampleRate: m.sampleRate }), node: null, held: false };
    const url = URL.createObjectURL(new Blob([m.processor], { type: 'application/javascript' }));
    await c.ctx.audioWorklet.addModule(url);
    if (cur !== c) return; // replaced while the module loaded
    c.node = new AudioWorkletNode(c.ctx, 'undertone-shared', {
      outputChannelCount: [2],
      processorOptions: { cap: m.cap, data: m.data, ctrl: m.ctrl },
    });
    c.node.connect(c.ctx.destination);
    c.ctx.resume(); // succeeds outright where autoplay is allowed
  };

  const onMessage = (e) => {
    const m = e.data;
    if (!m || typeof m.t !== 'string' || !m.t.startsWith('undertone:')) return;
    switch (m.t) {
      case 'undertone:open': open(m).catch((err) => console.error('undertone:', err)); break;
      case 'undertone:pause': if (cur) { cur.held = true; cur.ctx.suspend(); } break;
      case 'undertone:resume': if (cur) { cur.held = false; cur.ctx.resume(); } break;
      case 'undertone:close': close(); break;
    }
  };

  const Base = window.Worker;
  window.Worker = class extends Base {
    constructor(...args) {
      super(...args);
      this.addEventListener('message', onMessage);
    }
  };
})();
