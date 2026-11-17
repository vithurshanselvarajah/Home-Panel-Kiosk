// The system volume.
//
// The panel used to change the volume of the device program, which is the
// volume of the voice and the music on that one device. A person
// reaching for the volume on a panel expects the volume of the room, which
// is the system mixer.
//
// The mixer is found at run time, because which one is the default depends
// on the machine: PipeWire and PulseAudio both have a default sink, and
// ALSA has a master control that is usually the card's.
//
// The tools are tried in the order of how well they answer the question.
// The first one that works is used for the rest of the run.

const { execFile } = require("node:child_process");

// Runs a command and gives back what it said.
function run(command, args, timeoutMs = 2000) {
  return new Promise((resolve) => {
    execFile(command, args, { timeout: timeoutMs, maxBuffer: 1024 * 256 }, (error, stdout) => {
      resolve(error ? null : String(stdout || ""));
    });
  });
}

// The volume as a percentage, from a tool that prints it as a fraction.
//
// PipeWire and PulseAudio both print "Volume: 0.76" for a sink, and
// "Volume: 0.76 [MUTED]" when it is muted.
function parseFraction(text) {
  const match = /Volume:\s*([0-9.]+)/.exec(text || "");
  if (!match) return null;
  const value = Number(match[1]);
  if (!Number.isFinite(value)) return null;
  return Math.round(Math.max(0, Math.min(1, value)) * 100);
}

// Whether the mixer says it is muted.
//
// PipeWire and PulseAudio both put "[MUTED]" after the volume when the
// sink is muted, so the mute is read from the same answer as the
// volume rather than by asking again.
function parseMuted(text) {
  return /\[MUTED\]/i.test(text || "");
}

// The volume from amixer, which prints a percentage.
function parseAlsa(text) {
  const match = /\[(\d+)%\]/.exec(text || "");
  if (!match) return null;
  const value = Number(match[1]);
  return Number.isFinite(value) ? value : null;
}

// Athena exposes its speaker as a raw ALSA mixer element rather than the
// usual simple `Master` control. Convert that element's integer range to
// the same percentage used by the UI.
function parseAlsaElement(text) {
  const range = /max=(-?\d+)/.exec(text || "");
  const values = /: values=([-\d,]+)/.exec(text || "");
  if (!range || !values) return null;
  const maximum = Number(range[1]);
  const numbers = values[1].split(",").map(Number).filter(Number.isFinite);
  if (maximum <= 0 || numbers.length === 0) return null;
  const average = numbers.reduce((sum, value) => sum + value, 0) / numbers.length;
  return Math.round(Math.max(0, Math.min(100, average / maximum * 100)));
}

// Which mixer is the one, or null when there is none to change.
//
// The result is cached, because the tools are found once and a panel that
// asked every few hundred milliseconds would run a process every few
// hundred milliseconds.
let mixer = null;
let mixerLooked = false;

async function findMixer() {
  if (mixerLooked) return mixer;
  mixerLooked = true;

  // PipeWire is the modern one, and it is what a Raspberry Pi OS image
  // with a desktop uses.
  const pipe = await run("wpctl", ["get-volume", "@DEFAULT_AUDIO_SINK@"]);
  if (pipe !== null && parseFraction(pipe) !== null) {
    mixer = {
      read: async () => parseFraction(await run("wpctl", ["get-volume", "@DEFAULT_AUDIO_SINK@"])),
      write: (percent) => run("wpctl", ["set-volume", "@DEFAULT_AUDIO_SINK@", `${(percent / 100).toFixed(2)}`]),
      // The mute is read from the volume, because that is where it is
      // written: PipeWire prints "Volume: 0.65 [MUTED]" for a muted
      // sink. There is no command that says it on its own.
      // The microphone is muted on the source, which is where the room
      // comes in. Muting the sink would only silence the output, and the
      // mute is the microphone's.
      readMicMuted: async () => {
        const answer = await run("wpctl", ["get-volume", "@DEFAULT_AUDIO_SOURCE@"]);
        if (answer === null) return null;
        return parseMuted(answer);
      },
      writeMicMuted: (muted) => run("wpctl", ["set-mute", "@DEFAULT_AUDIO_SOURCE@", muted ? "1" : "0"]),
    };
    return mixer;
  }

  // PulseAudio is the older one, and is still what a lot of machines run.
  const pulse = await run("pactl", ["get-sink-volume", "@DEFAULT_SINK@"]);
  if (pulse !== null && parseFraction(pulse) !== null) {
    mixer = {
      read: async () => parseFraction(await run("pactl", ["get-sink-volume", "@DEFAULT_SINK@"])),
      write: (percent) => run("pactl", ["set-sink-volume", "@DEFAULT_SINK@", `${(percent / 100).toFixed(2)}`]),
      // The mute is read from the volume, because that is where it is
      // written: PulseAudio prints "Volume: 0.65 [MUTED]" for a muted
      // sink, the same as PipeWire does.
      readMicMuted: async () => {
        const answer = await run("pactl", ["get-source-volume", "@DEFAULT_SOURCE@"]);
        if (answer === null) return null;
        return parseMuted(answer);
      },
      writeMicMuted: (muted) => run("pactl", ["set-source-mute", "@DEFAULT_SOURCE@", muted ? "1" : "0"]),
    };
    return mixer;
  }

  // ALSA is the fallback. The master control of the first card is the
  // one, because the panel is talking about the whole machine.
  // `scontrols` accepts a card selector, not a mixer-control name. Asking
  // it to list the control therefore makes ALSA-only systems look as if
  // they have no mixer. Read the control itself and use amixer's normal
  // control lookup instead.
  const controls = await run("amixer", ["sget", "Master"]);
  if (controls !== null && /\[(\d+)%\]/.test(controls)) {
    mixer = {
      read: async () => parseAlsa(await run("amixer", ["sget", "Master"])),
      write: (percent) => run("amixer", ["sset", "Master", `${percent}%`]),
      // ALSA has no one control for the microphone, so the capture
      // controls are looked for: the first one that can be switched is
      // the microphone.
      readMicMuted: async () => {
        const answer = await run("amixer", ["sget", "Capture"]);
        if (answer === null) return null;
        return /\[off\]/.test(answer);
      },
      writeMicMuted: (muted) => run("amixer", ["sset", "Capture", muted ? "off" : "on"]),
    };
    return mixer;
  }

  const athenaMaster = await run("amixer", ["-c", "0", "cget", "name=Master Playback Volume"]);
  if (athenaMaster !== null && parseAlsaElement(athenaMaster) !== null) {
    // The Echo Show 8's speaker is on the amplifier's own control, which
    // the device program owns. The panel does not write it: the level is
    // applied in one place, in the samples, on a curve, and a level
    // applied in two places is a level nobody can reason about.
    //
    // So this mixer is read but not written. The device program applies
    // the level and tells the panel what it is.
    mixer = {
      read: async () => parseAlsaElement(await run("amixer", ["-c", "0", "cget", "name=Master Playback Volume"])),
      write: async () => null,
      readMicMuted: async () => {
        // The mute is a hardware latch, and the latch's position is what
        // the device program reports. There is no mixer control for it:
        // the microphone is cut at the board, not in the mixer.
        const answer = await run("amixer", ["-c", "0", "sget", "Capture"]);
        return answer === null ? null : /\[off\]/i.test(answer);
      },
      writeMicMuted: async () => null,
    };
    return mixer;
  }

  // There is no mixer this panel can change. The control is hidden rather
  // than shown at a value that would do nothing.
  mixer = null;
  return null;
}

// systemVolumePercent is the volume of the system, or null when there is
// no mixer to read.
async function systemVolumePercent() {
  const found = await findMixer();
  if (!found) return null;
  return found.read();
}

// setSystemVolume sets the volume of the system.
//
// It is best effort: a machine with no mixer is not a failure, it is a
// machine whose volume cannot be changed from here.
async function setSystemVolume(percent) {
  const value = Math.max(0, Math.min(100, Math.round(Number(percent))));
  if (!Number.isFinite(value)) return false;
  const found = await findMixer();
  if (!found) return false;
  await found.write(value);
  return true;
}

// setSystemMicMuted mutes the system microphone, or unmutes it.
//
// It is best effort, like the volume itself.
async function setSystemMicMuted(muted) {
  const found = await findMixer();
  if (!found || !found.writeMicMuted) return false;
  await found.writeMicMuted(Boolean(muted));
  return true;
}

module.exports = {
  systemVolumePercent,
  setSystemVolume,
  setSystemMicMuted,
};
