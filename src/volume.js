

const { execFile } = require("node:child_process");

function run(command, args, timeoutMs = 2000) {
  return new Promise((resolve) => {
    execFile(command, args, { timeout: timeoutMs, maxBuffer: 1024 * 256 }, (error, stdout) => {
      resolve(error ? null : String(stdout || ""));
    });
  });
}

function parseFraction(text) {
  const match = /Volume:\s*([0-9.]+)/.exec(text || "");
  if (!match) return null;
  const value = Number(match[1]);
  if (!Number.isFinite(value)) return null;
  return Math.round(Math.max(0, Math.min(1, value)) * 100);
}

function parseMuted(text) {
  return /\[MUTED\]/i.test(text || "");
}

function parseAlsa(text) {
  const match = /\[(\d+)%\]/.exec(text || "");
  if (!match) return null;
  const value = Number(match[1]);
  return Number.isFinite(value) ? value : null;
}

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

let mixer = null;
let mixerLooked = false;

async function findMixer() {
  if (mixerLooked) return mixer;
  mixerLooked = true;

  const pipe = await run("wpctl", ["get-volume", "@DEFAULT_AUDIO_SINK@"]);
  if (pipe !== null && parseFraction(pipe) !== null) {
    mixer = {
      read: async () => parseFraction(await run("wpctl", ["get-volume", "@DEFAULT_AUDIO_SINK@"])),
      write: (percent) => run("wpctl", ["set-volume", "@DEFAULT_AUDIO_SINK@", `${(percent / 100).toFixed(2)}`]),

      readMicMuted: async () => {
        const answer = await run("wpctl", ["get-volume", "@DEFAULT_AUDIO_SOURCE@"]);
        if (answer === null) return null;
        return parseMuted(answer);
      },
      writeMicMuted: (muted) => run("wpctl", ["set-mute", "@DEFAULT_AUDIO_SOURCE@", muted ? "1" : "0"]),
    };
    return mixer;
  }

  const pulse = await run("pactl", ["get-sink-volume", "@DEFAULT_SINK@"]);
  if (pulse !== null && parseFraction(pulse) !== null) {
    mixer = {
      read: async () => parseFraction(await run("pactl", ["get-sink-volume", "@DEFAULT_SINK@"])),
      write: (percent) => run("pactl", ["set-sink-volume", "@DEFAULT_SINK@", `${(percent / 100).toFixed(2)}`]),

      readMicMuted: async () => {
        const answer = await run("pactl", ["get-source-volume", "@DEFAULT_SOURCE@"]);
        if (answer === null) return null;
        return parseMuted(answer);
      },
      writeMicMuted: (muted) => run("pactl", ["set-source-mute", "@DEFAULT_SOURCE@", muted ? "1" : "0"]),
    };
    return mixer;
  }

  const controls = await run("amixer", ["sget", "Master"]);
  if (controls !== null && /\[(\d+)%\]/.test(controls)) {
    mixer = {
      read: async () => parseAlsa(await run("amixer", ["sget", "Master"])),
      write: (percent) => run("amixer", ["sset", "Master", `${percent}%`]),

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

    mixer = {
      read: async () => parseAlsaElement(await run("amixer", ["-c", "0", "cget", "name=Master Playback Volume"])),
      write: async () => null,
      readMicMuted: async () => {

        const answer = await run("amixer", ["-c", "0", "sget", "Capture"]);
        return answer === null ? null : /\[off\]/i.test(answer);
      },
      writeMicMuted: async () => null,
    };
    return mixer;
  }

  mixer = null;
  return null;
}

async function systemVolumePercent() {
  const found = await findMixer();
  if (!found) return null;
  return found.read();
}

async function setSystemVolume(percent) {
  const value = Math.max(0, Math.min(100, Math.round(Number(percent))));
  if (!Number.isFinite(value)) return false;
  const found = await findMixer();
  if (!found) return false;
  await found.write(value);
  return true;
}

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
