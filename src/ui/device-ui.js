

(function () {

  const CONNECTIONS = {
    unknown: { label: "Unknown", led: "unknown" },
    connected: { label: "Connected", led: "online" },
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

  const OVERLAY_LABELS = {
    listening: "Listening",
    thinking: "Listening",
    speaking: "Speaking",
  };

  const BUSY_FLICKER_MS = 1500;

  const OVERLAY_HOLD_MS = 5000;

  class VoiceOverlay {
    constructor(container) {
      this.container = container;
      this.orb = container.querySelector(".voice-overlay__orb");
      this.state = container.querySelector(".voice-overlay__state");
      this.heard = container.querySelector("#voice-overlay-heard");
      this.said = container.querySelector("#voice-overlay-said");
      this.mode = "off";
      this.level = 0;
      this.timer = null;

      this.heardText = "";
      this.saidText = "";

      this.shownSaid = 0;

      this.progress = 0;

      this.holding = false;

      this.busyUntil = 0;

      this.holdUntil = 0;

      this.gesture = null;
      this.installGesture();
    }

    setMode(mode) {
      if (mode === this.mode) return;

      const flickered = mode === "off" && this.mode !== "off" && this.busyUntil > Date.now();
      if (!flickered) {
        this.mode = mode;
      }

      if (mode === "off") {

        if (!this.holding) {
          this.holding = true;
          this.holdUntil = Date.now() + OVERLAY_HOLD_MS;
        }
        this.start();
        return;
      }

      this.container.hidden = false;
      this.container.dataset.state = mode;

      requestAnimationFrame(() => this.container.classList.add("is-active"));
      if (this.state) this.state.textContent = OVERLAY_LABELS[mode] || "";

      this.holding = false;
      this.holdUntil = 0;

      this.busyUntil = Date.now() + BUSY_FLICKER_MS;
      this.start();
    }

    setHeard(words) {
      this.heardText = words || "";
      if (this.heard) this.heard.textContent = this.heardText;
    }

    setSaid(words) {
      this.saidText = words || "";
      this.shownSaid = 0;
      if (this.said && !this.saidText) this.said.textContent = "";

      if (this.progress >= 1) {
        this.revealSaid();
      }
    }

    setProgress(progress) {
      this.progress = Math.max(0, Math.min(1, Number(progress) || 0));
    }

    setLevel(level) {
      this.level = Math.max(0, Math.min(1, level));
    }

    start() {
      if (this.timer) return;
      this.timer = setInterval(() => this.tick(), 60);
    }

    stop() {
      if (this.timer) clearInterval(this.timer);
      this.timer = null;
    }

    close() {
      this.container.classList.remove("is-active");
      this.container.hidden = true;
      this.stop();
      this.gesture = null;
      this.container.style.transform = "";
      this.container.style.opacity = "";
      this.heardText = "";
      this.saidText = "";
      this.shownSaid = 0;
      this.holdUntil = 0;
      this.holding = false;
      this.busyUntil = 0;
      this.lastReveal = 0;
      if (this.heard) this.heard.textContent = "";
      if (this.said) this.said.textContent = "";
      if (this.mode !== "off") {
        this.mode = "off";
        if (this.onClosed) this.onClosed();
      }
    }

    installGesture() {
      const start = (event) => {
        if (this.mode === "off") return;
        this.gesture = { x: event.clientX, y: event.clientY, moved: false };
      };
      const move = (event) => {
        if (!this.gesture) return;
        const dx = event.clientX - this.gesture.x;
        const dy = event.clientY - this.gesture.y;
        if (!this.gesture.moved) {
          if (Math.abs(dx) < 8 && Math.abs(dy) < 8) return;
          this.gesture.moved = true;
          this.container.classList.add("is-dismissing");
        }

        const distance = Math.hypot(dx, dy);
        const fade = Math.max(0, 1 - distance / 320);
        this.container.style.transform = `translate(${dx}px, ${dy}px)`;
        this.container.style.opacity = String(fade);
      };
      const end = () => {
        if (!this.gesture) return;
        const moved = this.gesture.moved;
        this.gesture = null;
        this.container.classList.remove("is-dismissing");
        if (!moved) {
          this.container.style.transform = "";
          this.container.style.opacity = "";
          return;
        }

        this.close();
      };
      this.container.addEventListener("pointerdown", start);
      window.addEventListener("pointermove", move);
      window.addEventListener("pointerup", end);
      window.addEventListener("pointercancel", end);
    }

    tick() {

      if (this.orb && this.mode !== "off") {
        const ring = this.orb.querySelector(".voice-overlay__ring");
        if (ring) {
          if (this.mode === "listening") {
            const scale = 1 + this.level * 0.5;
            ring.style.transform = `scale(${scale.toFixed(3)})`;
            ring.style.opacity = String(0.35 + this.level * 0.55);
          } else {
            ring.style.transform = "";
            ring.style.opacity = "";
          }
        }
      }
      this.revealSaid();

      if (this.holding && this.holdUntil && Date.now() > this.holdUntil && !this.gesture) {
        this.close();
      }
    }

    revealSaid() {
      if (!this.saidText || this.shownSaid >= this.saidText.length) return;

      const finished = this.progress >= 1;
      const target = finished ? 1 : Math.min(1, this.progress + 0.08);
      const wanted = Math.round(this.saidText.length * target);
      if (wanted <= this.shownSaid) return;

      let end = this.shownSaid;
      while (end < this.saidText.length && end < wanted) {
        end++;
      }

      while (end < this.saidText.length && this.saidText[end] !== " ") {
        end++;
      }

      if (end < this.saidText.length) {
        end++;
      }
      this.shownSaid = end;
      if (this.said) this.said.textContent = this.saidText.slice(0, this.shownSaid);
    }
  }

  const MOCK_SCRIPT = [
    { at: 0, connection: "connected", phase: "ready", listening: false, thinking: false, speaking: false, reason: "home assistant is connected" },
    { at: 5000, connection: "connected", phase: "ready", listening: true, thinking: false, speaking: false, reason: "home assistant is connected" },
    { at: 13000, connection: "connected", phase: "ready", listening: true, thinking: true, speaking: false, reason: "home assistant is connected" },
    { at: 17000, connection: "connected", phase: "ready", listening: false, thinking: false, speaking: true, reason: "home assistant is connected" },
    { at: 21000, connection: "connected", phase: "ready", listening: false, thinking: false, speaking: false, reason: "home assistant is connected" },
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
      const target = entry.listening ? 0.3 + Math.random() * 0.55 : 0;
      level += (target - level) * 0.22;
      if (level < 0.005) level = 0;
      const state = {
        connection: entry.connection,
        phase: entry.phase,
        listening: entry.listening,
        thinking: entry.thinking,
        speaking: entry.speaking,
        reason: entry.reason,
        muted: false,
        level,
      };
      for (const listener of listeners) listener(state);
    }

    return {
      onChange(listener) { listeners.push(listener); },
      start() {

        setInterval(() => tick(performance.now()), 50);
      },
    };
  }

  function create(options) {
    const overlay = options.overlay ? new VoiceOverlay(options.overlay) : null;
    if (overlay) {

      overlay.onClosed = () => {
        if (options.onOverlayClosed) options.onOverlayClosed();
      };
    }

    function paint(state) {

      const key = state.pairing && state.connection === "pending" ? "pairing" : state.connection;
      const connection = CONNECTIONS[key] || CONNECTIONS.unknown;
      if (options.row) options.row.hidden = false;
      if (options.state) {
        options.state.textContent = connection.label;
        options.state.title = state.connection_reason || "";
      }
      if (options.led) options.led.dataset.status = connection.led;

      if (options.statusLed) options.statusLed.dataset.status = connection.led;
      if (options.statusLabel) options.statusLabel.textContent = connection.label;
      if (options.statusReason) options.statusReason.textContent = state.connection_reason || "Not connected to Home Assistant.";
    }

    function paintOverlay(state) {
      if (!overlay) return;
      let mode = "off";
      if (!state.muted) {
        if (state.speaking) mode = "speaking";
        else if (state.thinking) mode = "thinking";
        else if (state.listening) mode = "listening";
      }

      const transcript = state.transcript || "";
      if (transcript && transcript !== overlay.heardText) {
        overlay.setHeard(transcript);
      }

      const spoken = state.spoken || "";
      if (spoken && spoken !== overlay.saidText) {
        overlay.setSaid(spoken);
      }

      overlay.setProgress(state.spoken_progress || 0);

      if (mode === "off") {
        overlay.setMode("off");
        return;
      }

      if (mode === "listening" && overlay.mode !== "listening") {
        overlay.setHeard("");
        overlay.setSaid("");
      }
      overlay.setMode(mode);
      if (mode === "listening") overlay.setLevel((state.level || 0) / 100);
    }

    function apply(state) {
      paint(state);
      paintOverlay(state);
    }

    return { apply, overlay };
  }

  window.VoiceUI = { create, createMockSource, CONNECTIONS, PHASES, VoiceOverlay };
})();
