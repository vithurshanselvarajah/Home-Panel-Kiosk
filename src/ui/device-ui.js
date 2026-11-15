// Voice assistant UI.
//
// This is the shape of the voice assistant interface, with a mock data
// source. The state comes from the device program, which owns the link to
// Home Assistant.
//
// One rule shapes this file: every state that is not known is reported as
// unknown, never as its opposite. So an unknown connection is drawn grey and
// says "Unknown", not "Connected".

(function () {
  // Connection is what the drawer reports: whether this device can reach
  // Home Assistant. The phases that come with it are what the on-screen
  // indicator reacts to.
  //
  // There is no approval step and no credential: Home Assistant finds the
  // panel on the network and asks it for its key, and the panel either has
  // one or it does not.
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

  // ── The overlay ─────────────────────────────────────────────────────────
  // A full-screen overlay that covers the screen while the assistant is
  // working, in the way a smart display does.
  //
  // It shows the words: what is being heard while it listens and what is
  // being said while it answers. The text is carried from one state to
  // the next, so the screen reads as one conversation rather than three
  // screens: the words that were heard stay on the screen while it
  // thinks, and are replaced by the answer when it comes.

  const OVERLAY_LABELS = {
    listening: "Listening",
    thinking: "Listening",
    speaking: "Speaking",
  };

  // BUSY_FLICKER_MS is how long a gap in the work is a flicker and not
  // the end of it.
  //
  // The microphone closes before the reply starts, and the run ends
  // before the audio has all arrived. Those gaps are a moment long, and
  // the state they leave is held rather than blanked.
  const BUSY_FLICKER_MS = 1500;

  // OVERLAY_HOLD_MS is how long the overlay stays up after the
  // assistant has finished.
  //
  // Five seconds is long enough to read the answer and long enough not
  // to be in the way. A person can swipe it away before that.
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
      // The words heard and the words being said.
      this.heardText = "";
      this.saidText = "";
      // shownSaid is how much of the answer has been revealed.
      this.shownSaid = 0;
      // progress is how far through the reply's audio the panel is.
      this.progress = 0;
      // holding says the hold is running: the work is done and the
      // overlay is waiting out the rest of its time.
      this.holding = false;
      // busyUntil is how long a gap in the work is a flicker and not
      // the end of it.
      this.busyUntil = 0;
      // holdUntil is when the overlay may go away.
      //
      // It stays up for a while after the assistant has finished, so the
      // answer can be read. A person can swipe it away before that.
      this.holdUntil = 0;
      // The gesture that is dismissing the overlay.
      this.gesture = null;
      this.installGesture();
    }

    // setMode shows or hides the overlay.
    //
    // The words are kept while the overlay is up, so moving from
    // listening to thinking does not blank the screen.
    //
    // "Off" does not close it at once. The assistant's flags flicker:
    // the microphone closes before the reply starts, and there is a
    // moment where nothing is happening and the answer has not begun.
    // Closing then would mean the overlay appeared and vanished before
    // the person saw the answer. So the overlay is held for a while
    // first, and only then does it go.
    setMode(mode) {
      if (mode === this.mode) return;

      // The state arrives many times a second, and the assistant's flags
      // flicker: the microphone closes before the reply starts, and the
      // run ends before the audio has all arrived.
      //
      // So the state is not simply the last one it was given. A gap in
      // the work is a flicker and the state is held rather than
      // blanked, so the listening does not give way to nothing and back
      // again, and the answer is not missed because the microphone
      // closed for a moment.
      //
      // The end of the work is not a flicker. The state goes to off and
      // the hold runs from there.
      const flickered = mode === "off" && this.mode !== "off" && this.busyUntil > Date.now();
      if (!flickered) {
        this.mode = mode;
      }

      if (mode === "off") {
        // There is no work now, so the hold runs and the overlay goes
        // away when it is over.
        //
        // The hold is only started once: the state arrives many times a
        // second, and a hold that was restarted by each of them would
        // never run out.
        if (!this.holding) {
          this.holding = true;
          this.holdUntil = Date.now() + OVERLAY_HOLD_MS;
        }
        this.start();
        return;
      }

      this.container.hidden = false;
      this.container.dataset.state = mode;
      // One frame with the class applied is what starts the transition.
      requestAnimationFrame(() => this.container.classList.add("is-active"));
      if (this.state) this.state.textContent = OVERLAY_LABELS[mode] || "";
      // There is work, so the hold does not run.
      this.holding = false;
      this.holdUntil = 0;
      // A gap in the work is a flicker for as long as the work lasts.
      this.busyUntil = Date.now() + BUSY_FLICKER_MS;
      this.start();
    }

    // setHeard says what was heard.
    //
    // It is shown above the answer, in the quieter colour, and it stays
    // there for as long as the overlay is up.
    setHeard(words) {
      this.heardText = words || "";
      if (this.heard) this.heard.textContent = this.heardText;
    }

    // setSaid says what is being said.
    setSaid(words) {
      this.saidText = words || "";
      this.shownSaid = 0;
      if (this.said && !this.saidText) this.said.textContent = "";
      // The answer is shown at once if the reply has already been
      // played, which is what happens when the text arrives after the
      // audio: the words would otherwise wait for the next tick, and
      // the tick that reveals them is the one that closes the overlay.
      if (this.progress >= 1) {
        this.revealSaid();
      }
    }

    // setProgress says how far through the reply the panel is.
    //
    // The reply's text arrives all at once but its audio arrives as it
    // is generated and is played as it arrives. The words are shown in
    // step with the audio, so the answer appears on the screen as it is
    // spoken rather than all at once at the beginning.
    setProgress(progress) {
      this.progress = Math.max(0, Math.min(1, Number(progress) || 0));
    }

    // setLevel sets the microphone level, which the orb breathes with.
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

    // close puts the overlay away.
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

    // installGesture makes the overlay swipeable away.
    //
    // It follows the finger and fades as it goes, so the gesture reads
    // as the gesture rather than as a button press. Any direction will
    // do it: up, down, left or right.
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
        // The overlay follows the finger and fades as it goes.
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
        // It was swiped, so it goes away.
        this.close();
      };
      this.container.addEventListener("pointerdown", start);
      window.addEventListener("pointermove", move);
      window.addEventListener("pointerup", end);
      window.addEventListener("pointercancel", end);
    }

    // tick moves the orb and reveals the answer.
    //
    // While it listens the orb follows the microphone level, so it is
    // clear that the panel is hearing something. While it thinks and
    // speaks it turns over on its own.
    tick() {
      // The overlay is still up while the mode is off: the mode goes
      // off when the assistant has finished, and the overlay is held
      // for a few seconds after that before it goes away.
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
      // The overlay goes away once the hold is over and nobody is
      // touching it.
      //
      // The hold only runs when there is no work: the overlay stays up
      // for as long as the assistant is working, however long that is.
      if (this.holding && this.holdUntil && Date.now() > this.holdUntil && !this.gesture) {
        this.close();
      }
    }

    // revealSaid shows more of the answer as it is spoken.
    //
    // How much is shown follows the progress of the reply's audio, so
    // the words and the voice stay together.
    revealSaid() {
      if (!this.saidText || this.shownSaid >= this.saidText.length) return;
      // The text is shown in step with the audio, and it is finished off
      // once the audio has all been played.
      //
      // It keeps going after the speaking stops, because the words and
      // the voice do not end at quite the same moment: the device's
      // speaking flag drops when the speaker has drained the reply, and
      // the last of the words may not have been shown yet.
      const finished = this.progress >= 1;
      const target = finished ? 1 : Math.min(1, this.progress + 0.08);
      const wanted = Math.round(this.saidText.length * target);
      if (wanted <= this.shownSaid) return;
      // How far the text has been read, which is roughly how much of
      // the audio has been played.
      let end = this.shownSaid;
      while (end < this.saidText.length && end < wanted) {
        end++;
      }
      // A word is shown whole, so the text goes on to the end of the
      // word that is being spoken.
      //
      // Stopping at the space in front of the word would cut the word
      // in half, and stopping there when the answer is over would lose
      // its last word: "10:18 PM" would be shown as "10:18 ".
      while (end < this.saidText.length && this.saidText[end] !== " ") {
        end++;
      }
      // And on past the space, so the next word starts cleanly.
      if (end < this.saidText.length) {
        end++;
      }
      this.shownSaid = end;
      if (this.said) this.said.textContent = this.saidText.slice(0, this.shownSaid);
    }
  }

  // ── Mock source ────────────────────────────────────────────────────────
  // This is the only place the fake state lives. It is here so the interface
  // can be judged on a screen before any protocol code exists.

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
        // A timer rather than an animation frame: the mock has to keep
        // running when the window is not focused.
        setInterval(() => tick(performance.now()), 50);
      },
    };
  }

  // ── Wiring ─────────────────────────────────────────────────────────────

  function create(options) {
    const overlay = options.overlay ? new VoiceOverlay(options.overlay) : null;
    if (overlay) {
      // The main process sizes the view the overlay is drawn in, so it
      // has to be told when the overlay goes away.
      overlay.onClosed = () => {
        if (options.onOverlayClosed) options.onOverlayClosed();
      };
    }

    function paint(state) {
      // A device that is asking to be admitted is shown as pairing, which
      // is what it is doing. Everything else is shown as the connection
      // the sidecar reported.
      const key = state.pairing && state.connection === "pending" ? "pairing" : state.connection;
      const connection = CONNECTIONS[key] || CONNECTIONS.unknown;
      if (options.row) options.row.hidden = false;
      if (options.state) {
        options.state.textContent = connection.label;
        options.state.title = state.connection_reason || "";
      }
      if (options.led) options.led.dataset.status = connection.led;

      // The same state is repeated at the top of the voice tab, so the
      // settings page says what the drawer says.
      if (options.statusLed) options.statusLed.dataset.status = connection.led;
      if (options.statusLabel) options.statusLabel.textContent = connection.label;
      if (options.statusReason) options.statusReason.textContent = state.connection_reason || "Not connected to Home Assistant.";
    }
    // The overlay is the big one: it covers the screen while the
    // assistant is working and it shows the words.
    //
    // What the device is doing is read from the flags, not from the
    // phase. The phase is the state of the link to Home Assistant — ready,
    // pending, refused, disconnected — and a device whose link is up
    // reports "ready" whether it is listening or not. The flags are where
    // the link's own state is kept, and they are what the overlay wants:
    // the microphone being open, a reply being played, Home Assistant
    // waiting for Home Assistant.
    function paintOverlay(state) {
      if (!overlay) return;
      let mode = "off";
      if (!state.muted) {
        if (state.speaking) mode = "speaking";
        else if (state.thinking) mode = "thinking";
        else if (state.listening) mode = "listening";
      }

      // The words are given to the overlay before the mode is.
      //
      // The reply ends and the speaking stops at the same moment, so
      // the mode goes off exactly when the last of the words is due to
      // be shown. The words have to be given to the overlay first, or
      // the answer stops a word short: "Sorry, I am not aware of any
      // device called a jok".
      //
      // What was heard goes above, and it stays there: the question is
      // the context for the answer.
      const transcript = state.transcript || "";
      if (transcript && transcript !== overlay.heardText) {
        overlay.setHeard(transcript);
      }

      // What is being said goes below.
      //
      // The text arrives all at once, so it is revealed in step with the
      // audio rather than all at once at the beginning.
      const spoken = state.spoken || "";
      if (spoken && spoken !== overlay.saidText) {
        overlay.setSaid(spoken);
      }
      // How far through the reply the panel is, so the answer's words
      // are shown as they are spoken.
      //
      // This is the last of the reply, so the progress is all of it and
      // the answer is shown whole.
      overlay.setProgress(state.spoken_progress || 0);

      // The words are only cleared when the overlay goes away, so the
      // text that was heard stays on the screen while the assistant
      // thinks about it.
      if (mode === "off") {
        overlay.setMode("off");
        return;
      }
      // A new turn clears what the last one said.
      //
      // The words are kept while the overlay is up, which is what keeps
      // the question on the screen while the assistant thinks about it.
      // But the next question comes with the overlay up again and the
      // old answer still on it, and the answer is only replaced when
      // the new one arrives — so for a moment the panel says the
      // answer to the last question while it is listening to this one.
      //
      // A turn that starts with no words of its own clears the old ones,
      // and the text comes back when the new words arrive.
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
