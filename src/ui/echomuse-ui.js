// EchoMuse UI.
//
// This is the shape of the EchoMuse interface, with a mock data source. The
// SDK is not wired up yet, so the state comes from the mock below. When the
// SDK lands, replace the mock source and nothing else has to change.
//
// The states come from the SDK's `echomuse/state` package, and one of its
// rules shapes this file: every state that is not known is reported as
// unknown, never as its opposite. So an unknown connection is drawn grey and
// says "Unknown", not "Connected".

(function () {
  // Connection is what the drawer reports: whether this device can reach a
  // controller and has been admitted by one. The phases that come with it
  // are what the on-screen indicator reacts to.
  const CONNECTIONS = {
    unknown: { label: "Unknown", led: "unknown" },
    connected: { label: "Connected", led: "online" },
    unpaired: { label: "Unpaired", led: "connecting" },
    disabled: { label: "Disabled", led: "offline" },
    refused: { label: "Refused", led: "offline" },
    disconnected: { label: "Disconnected", led: "offline" },
  };

  const PHASES = {
    unknown: { label: "Unknown" },
    idle: { label: "Idle" },
    listening: { label: "Listening" },
    thinking: { label: "Thinking" },
    speaking: { label: "Speaking" },
    muted: { label: "Muted" },
  };

  // ── The indicator ──────────────────────────────────────────────────────
  // A row of dots along the bottom of the view, the way a voice assistant
  // shows it is listening. The dots move and scale with the microphone
  // level, and a different animation plays while a response is playing.
  //
  // It is an indicator, not a row of LEDs. An Echo's LEDs report the
  // controller's own animation; these report what this device is doing, and
  // they are only on screen while it is doing something.

  const DOT_COUNT = 7;
  const DOT_SPACING = 26;

  const MODE_LABELS = {
    listening: "Listening",
    speaking: "Speaking",
  };

  class VoiceIndicator {
    constructor(container) {
      this.container = container;
      this.canvas = container.querySelector("canvas");
      this.label = container.querySelector(".voice-indicator__label");
      this.context = this.canvas.getContext("2d");
      this.dots = new Array(DOT_COUNT).fill(0);
      this.level = 0;
      this.mode = "off";
      this.timer = null;
      this.lastFrame = 0;
      this.phase = 0;
      this.width = 0;
      this.height = 0;
    }

    setLevel(level) {
      this.level = Math.max(0, Math.min(1, level));
    }

    // mode is "off", "listening" or "speaking".
    setMode(mode) {
      if (mode === this.mode) return;
      this.mode = mode;
      this.container.hidden = mode === "off";
      if (this.label) this.label.textContent = MODE_LABELS[mode] || "";
      if (mode === "off") {
        this.dots.fill(0);
        this.stop();
        this.context.clearRect(0, 0, this.canvas.width, this.canvas.height);
        return;
      }
      this.resize();
      this.lastFrame = performance.now();
      this.start();
    }

    // The animation is driven by a timer rather than by requestAnimationFrame.
    // A kiosk window can be occluded or unfocused, and Chromium suspends
    // animation frames entirely when that happens, which would freeze the
    // indicator on a stale reading. A timer keeps it honest.
    start() {
      if (this.timer) return;
      this.timer = setInterval(() => this.tick(performance.now()), 50);
    }

    stop() {
      if (this.timer) clearInterval(this.timer);
      this.timer = null;
    }

    // The indicator sits above the on-screen keyboard when it is up.
    lift(bottom) {
      this.container.style.bottom = `${bottom}px`;
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
      if (this.mode === "off") return;
      const delta = Math.min(100, now - this.lastFrame || 16);
      this.lastFrame = now;
      this.phase += delta / 1000;

      if (this.mode === "listening") {
        // Each dot takes the level with a little delay, so the row reads as a
        // wave travelling across rather than everything jumping at once.
        this.dots.pop();
        this.dots.unshift(this.level * (0.7 + Math.random() * 0.6));
        for (let i = 0; i < this.dots.length; i++) {
          const falloff = 1 - Math.abs(i - (this.dots.length - 1) / 2) / this.dots.length;
          this.dots[i] *= 0.82 + falloff * 0.16;
        }
      } else {
        // Speaking has no microphone level to show, so the dots pulse along
        // instead, the way a voice assistant shows it is answering.
        for (let i = 0; i < this.dots.length; i++) {
          const offset = Math.sin(this.phase * 4 - i * 0.6);
          this.dots[i] = 0.35 + Math.max(0, offset) * 0.65;
        }
      }

      this.draw();
    }

    draw() {
      const context = this.context;
      const width = this.width || this.canvas.clientWidth;
      const height = this.height || this.canvas.clientHeight;
      if (!width || !height) return;
      context.clearRect(0, 0, width, height);

      const centreX = width / 2;
      const baseline = height * 0.5;
      const maxRadius = 9;
      const span = (this.dots.length - 1) * DOT_SPACING;

      for (let i = 0; i < this.dots.length; i++) {
        const value = Math.max(0, Math.min(1, this.dots[i]));
        const x = centreX - span / 2 + i * DOT_SPACING;
        const radius = 3 + value * maxRadius;
        // The middle of the row is the brightest, so the eye goes there.
        const centre = 1 - Math.abs(i - (this.dots.length - 1) / 2) / this.dots.length;
        const alpha = 0.3 + centre * 0.6 * (0.35 + value * 0.65);
        context.beginPath();
        context.fillStyle = `rgba(143, 227, 194, ${alpha.toFixed(3)})`;
        context.arc(x, baseline, radius, 0, Math.PI * 2);
        context.fill();
      }
    }
  }

  // ── Mock source ────────────────────────────────────────────────────────
  // This is the only place the fake state lives. It is here so the interface
  // can be judged on a screen before any protocol code exists.

  const MOCK_SCRIPT = [
    { at: 0, connection: "connected", phase: "idle", reason: "the controller admitted this device" },
    { at: 5000, connection: "connected", phase: "listening", reason: "the controller admitted this device" },
    { at: 13000, connection: "connected", phase: "thinking", reason: "the controller admitted this device" },
    { at: 17000, connection: "connected", phase: "speaking", reason: "the controller admitted this device" },
    { at: 21000, connection: "connected", phase: "idle", reason: "the controller admitted this device" },
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
        connection: entry.connection,
        phase: entry.phase,
        reason: entry.reason,
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
    const indicator = new VoiceIndicator(options.container);

    function paint(state) {
      const connection = CONNECTIONS[state.connection] || CONNECTIONS.unknown;
      options.row.hidden = false;
      options.state.textContent = connection.label;
      options.state.title = state.reason || "";
      options.led.dataset.status = connection.led;

      // The same state is repeated at the top of the EchoMuse tab, so the
      // settings page says what the drawer says.
      if (options.statusLed) options.statusLed.dataset.status = connection.led;
      if (options.statusLabel) options.statusLabel.textContent = connection.label;
      if (options.statusReason) options.statusReason.textContent = state.reason || "Not connected to a controller.";
    }
    // The indicator is only on screen while the device is listening or
    // speaking. It is not a row of status LEDs that sits there all the time.
    function paintIndicator(state) {
      let mode = "off";
      if (!state.muted && state.phase === "listening") mode = "listening";
      else if (!state.muted && state.phase === "speaking") mode = "speaking";
      indicator.setMode(mode);
      if (mode === "listening") indicator.setLevel(state.level);
    }

    function apply(state) {
      paint(state);
      paintIndicator(state);
    }

    return { apply, indicator };
  }

  window.EchoMuseUI = { create, createMockSource, CONNECTIONS, PHASES };
})();
