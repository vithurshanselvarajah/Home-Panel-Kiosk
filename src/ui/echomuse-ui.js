// EchoMuse UI.
//
// This is the shape of the EchoMuse interface, with a mock data source. The
// SDK is not wired up yet, so the state and the level come from the mock
// below. When the SDK lands, replace the mock source and nothing else has to
// change.
//
// The states and their meanings come from the SDK's `echomuse/state` package.
// Two of its rules shape this file:
//
//   - Every state that is not known is reported as unknown, never as its
//     opposite. So an unknown phase is drawn grey and says "Unknown".
//   - What it reports about listening is what it is actually doing. So the
//     badge only says "local" when the device really is scoring its own
//     wake word, and never claims privacy it does not have.

(function () {
  const PHASES = {
    unknown: { label: "Unknown", led: "unknown" },
    idle: { label: "Idle", led: "online" },
    ready: { label: "Ready", led: "online" },
    listening: { label: "Listening", led: "listening" },
    thinking: { label: "Thinking", led: "connecting" },
    speaking: { label: "Speaking", led: "connecting" },
    pending: { label: "Awaiting approval", led: "connecting" },
    refused: { label: "Refused", led: "offline" },
    disconnected: { label: "Disconnected", led: "offline" },
    muted: { label: "Muted", led: "offline" },
  };

  const LISTEN_MODES = {
    unknown: { label: "unknown" },
    local: { label: "local" },
    stream: { label: "streaming" },
    degraded: { label: "no wake word" },
  };

  const LISTEN_TITLES = {
    local: "The device scores its own wake word. No audio leaves it until the wake word fires.",
    stream: "Audio is streaming to the controller continuously.",
    degraded: "The device cannot run a wake word and works by button only.",
    unknown: "The device has not said how it listens.",
  };

  // ── The wave ───────────────────────────────────────────────────────────
  // A band of bars across the bottom of the view. Each bar holds the level
  // it saw and decays, so the band reads as a wave rather than a jittering
  // bar chart.
  //
  // The SDK only exposes a peak level, not the samples, so this is a level
  // meter shaped like a waveform rather than the waveform itself.

  const BAR_COUNT = 48;
  const BAR_DECAY = 0.86;

  class VoiceWave {
    constructor(canvas) {
      this.canvas = canvas;
      this.context = canvas.getContext("2d");
      this.bars = new Array(BAR_COUNT).fill(0);
      this.level = 0;
      this.active = false;
      this.handle = null;
      this.timer = null;
      this.lastFrame = 0;
      this.width = 0;
      this.height = 0;
    }

    setLevel(level) {
      this.level = Math.max(0, Math.min(1, level));
    }

    // Show the wave only when the microphone is genuinely open. A wave that
    // is not measuring anything would be the same lie as reporting a state
    // that is not true.
    setActive(active) {
      if (active === this.active) return;
      this.active = active;
      this.canvas.hidden = !active;
      if (active) {
        // The canvas has to be visible before it can be measured, so the
        // hidden attribute is cleared first and the size is taken after.
        this.resize();
        this.lastFrame = performance.now();
        this.start();
      } else {
        this.bars.fill(0);
        this.stop();
        this.context.clearRect(0, 0, this.canvas.width, this.canvas.height);
      }
    }

    // The animation is driven by a timer rather than by requestAnimationFrame.
    // A kiosk window can be occluded or unfocused, and Chromium suspends
    // animation frames entirely when that happens, which would freeze the
    // wave on a stale reading. A timer keeps it honest.
    start() {
      if (this.timer) return;
      this.timer = setInterval(() => this.tick(performance.now()), 50);
    }

    stop() {
      if (this.timer) clearInterval(this.timer);
      this.timer = null;
      if (this.handle) cancelAnimationFrame(this.handle);
      this.handle = null;
    }

    // The wave sits above the on-screen keyboard when it is up.
    lift(bottom) {
      this.canvas.style.bottom = `${bottom}px`;
    }

    resize() {
      const ratio = window.devicePixelRatio || 1;
      const width = this.canvas.clientWidth;
      const height = this.canvas.clientHeight;
      if (!width || !height) return;
      this.canvas.width = Math.round(width * ratio);
      this.canvas.height = Math.round(height * ratio);
      this.context.setTransform(ratio, 0, 0, ratio, 0, 0);
      this.width = width;
      this.height = height;
    }

    tick(now) {
      if (!this.active) return;
      const delta = Math.min(100, now - this.lastFrame || 16);
      this.lastFrame = now;

      // The newest bar takes the current level, and every bar decays.
      this.bars.shift();
      this.bars.push(this.level);
      for (let i = 0; i < this.bars.length - 1; i++) {
        this.bars[i] *= Math.pow(BAR_DECAY, delta / 16);
      }

      this.draw();
    }

    draw() {
      const context = this.context;
      const width = this.width || this.canvas.clientWidth;
      const height = this.height || this.canvas.clientHeight;
      if (!width || !height) return;
      context.clearRect(0, 0, width, height);

      const gap = 3;
      const barWidth = Math.max(2, (width - gap * (this.bars.length - 1)) / this.bars.length);
      const baseline = height * 0.82;
      const maxHeight = height * 0.66;

      for (let i = 0; i < this.bars.length; i++) {
        const value = this.bars[i];
        if (value <= 0.001) continue;
        const barHeight = Math.max(2, value * maxHeight);
        const x = i * (barWidth + gap);
        // Older bars fade, so the eye follows the leading edge.
        const age = i / this.bars.length;
        const alpha = 0.18 + age * 0.72;
        context.fillStyle = `rgba(143, 227, 194, ${alpha.toFixed(3)})`;
        const radius = Math.min(barWidth / 2, 2);
        context.beginPath();
        context.roundRect(x, baseline - barHeight, barWidth, barHeight, radius);
        context.fill();
      }
    }
  }

  // ── Mock source ────────────────────────────────────────────────────────
  // This is the only place the fake state lives. It is here so the interface
  // can be judged on a screen before any protocol code exists.

  const MOCK_SCRIPT = [
    { at: 0, phase: "ready", listen: "local", reason: "the controller admitted this device" },
    { at: 5000, phase: "listening", listen: "local", reason: "the microphone is open" },
    { at: 13000, phase: "thinking", listen: "local", reason: "the wake word fired" },
    { at: 17000, phase: "speaking", listen: "local", reason: "a response is playing" },
    { at: 21000, phase: "idle", listen: "local", reason: "" },
  ];

  function createMockSource() {
    let startedAt = 0;
    let level = 0;
    const listeners = [];

    function tick(now) {
      if (!startedAt) startedAt = now;
      const elapsed = now - startedAt;
      let entry = MOCK_SCRIPT[0];
      for (const step of MOCK_SCRIPT) {
        if (elapsed >= step.at) entry = step;
      }
      const target = entry.phase === "listening" ? 0.3 + Math.random() * 0.55 : 0;
      level += (target - level) * 0.22;
      if (level < 0.005) level = 0;
      const state = {
        phase: entry.phase,
        reason: entry.reason,
        listen: entry.listen,
        muted: false,
        level,
      };
      for (const listener of listeners) listener(state);
    }

    return {
      onChange(listener) { listeners.push(listener); },
      start() {
        // A timer rather than an animation frame: the mock has to keep
        // running when the window is not focused.
        setInterval(() => tick(performance.now()), 50);
      },
    };
  }

  // ── Wiring ─────────────────────────────────────────────────────────────

  function create(options) {
    const wave = new VoiceWave(options.canvas);

    function paint(state) {
      const phase = PHASES[state.phase] || PHASES.unknown;
      const listen = LISTEN_MODES[state.listen] || LISTEN_MODES.unknown;

      options.row.hidden = false;
      options.phase.textContent = phase.label;
      options.phase.title = state.reason || "";
      options.led.dataset.status = phase.led;

      options.listen.hidden = false;
      options.listen.textContent = listen.label;
      options.listen.dataset.listen = state.listen in LISTEN_MODES ? state.listen : "unknown";
      options.listen.title = LISTEN_TITLES[options.listen.dataset.listen] || "";
    }

    // The wave shows while the microphone is open. While the device is
    // thinking or speaking the microphone is not the story, and while it is
    // muted or degraded there is nothing honest to draw.
    function paintWave(state) {
      wave.setActive(state.phase === "listening" && !state.muted);
      if (wave.active) wave.setLevel(state.level);
    }

    function apply(state) {
      paint(state);
      paintWave(state);
    }

    return { apply, wave };
  }

  window.EchoMuseUI = { create, createMockSource, PHASES, LISTEN_MODES };
})();
