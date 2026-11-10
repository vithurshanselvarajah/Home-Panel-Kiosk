// The system volume.
//
// The panel used to change the volume of the device program, which is the
// volume of the EchoMuse voice and the music on that one device. A person
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

// The volume from amixer, which prints a percentage.
function parseAlsa(text) {
  const match = /\[(\d+)%\]/.exec(text || "");
  if (!match) return null;
  const value = Number(match[1]);
  return Number.isFinite(value) ? value : null;
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
    };
    return mixer;
  }

  // PulseAudio is the older one, and is still what a lot of machines run.
  const pulse = await run("pactl", ["get-sink-volume", "@DEFAULT_SINK@"]);
  if (pulse !== null && parseFraction(pulse) !== null) {
    mixer = {
      read: async () => parseFraction(await run("pactl", ["get-sink-volume", "@DEFAULT_SINK@"])),
      write: (percent) => run("pactl", ["set-sink-volume", "@DEFAULT_SINK@", `${(percent / 100).toFixed(2)}`]),
    };
    return mixer;
  }

  // ALSA is the fallback. The master control of the first card is the
  // one, because the panel is talking about the whole machine.
  const controls = await run("amixer", ["scontrols", "Simple mixer control 'Master'"]);
  if (controls !== null && /Simple mixer control 'Master'/.test(controls)) {
    mixer = {
      read: async () => parseAlsa(await run("amixer", ["sget", "Master"])),
      write: (percent) => run("amixer", ["sset", "Master", `${percent}%`]),
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

module.exports = { systemVolumePercent, setSystemVolume };
