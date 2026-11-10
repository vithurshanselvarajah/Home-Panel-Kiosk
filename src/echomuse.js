// The EchoMuse client.
//
// The EchoMuse link lives in a sidecar: a separate process that owns the
// controller connection, the microphone, the speaker and the wake word. The
// application only renders what the sidecar reports.
//
// The two speak through a state file. The file is written atomically by the
// sidecar and read here on a timer, which is simple and survives the sidecar
// being restarted. A socket would push the state the moment it changed, and
// is the better shape, but a file is enough for a display that only redraws
// when it is told something changed.

const { spawn } = require("node:child_process");
const fs = require("node:fs");
const fsp = require("node:fs/promises");
const path = require("node:path");

// How often the state file is read. The sidecar writes it when the state
// changes, so this is only how quickly a change is noticed.
const POLL_INTERVAL_MS = 250;

// Where the sidecar lives. It is beside the Electron binary when the app is
// packaged, and in the repository's vendor directory when it is not.
const BINARY_NAME = "echomuse-device";

// How long the sidecar is given to stop before it is killed.
//
// The sidecar releases the microphone and the speaker when it exits, so
// the replacement needs the old one to have gone. The wait is bounded so
// a sidecar that hangs does not stop the application from starting.
const STOP_TIMEOUT_MS = 3000;

// The state the sidecar reports. These are the fields the interface draws.
const DEFAULT_STATE = {
  connection: "unknown",
  connection_reason: "",
  phase: "unknown",
  phase_reason: "",
  listen: "unknown",
  listening: false,
  speaking: false,
  thinking: false,
  muted: false,
  volume: 0,
  level: 0,
  pattern: "",
  palette: [],
  audio_inputs: [],
  audio_outputs: [],
  selected_audio_input: "",
  selected_audio_output: "",
  monitoring: false,
  last_error: "",
};

/**
 * Finds the sidecar binary.
 *
 * The sidecar is a separate program, so it cannot live inside the asar and
 * it has to be somewhere the application can execute it from. It sits
 * beside the executable, which is where the packaging step puts it.
 *
 * @param {string} appPath the directory the application was started from
 * @param {string} resourcesPath the directory Electron's resources live in
 * @param {string} execPath the path of the running executable
 * @returns {string|null} the path to the binary, or null when there is none
 */
function findSidecar(appPath, resourcesPath, execPath) {
  const candidates = [
    // Beside the executable, which is where the package puts it.
    path.join(path.dirname(execPath || ""), BINARY_NAME),
    // Beside the resources directory, for a build that puts it there.
    path.join(path.dirname(resourcesPath), BINARY_NAME),
    path.join(resourcesPath, BINARY_NAME),
    path.join(path.dirname(appPath), BINARY_NAME),
    // The repository's own copy, for a development run.
    path.join(appPath, "vendor", `echomuse-device-linux-${process.arch === "x64" ? "amd64" : process.arch}`),
  ];
  for (const candidate of candidates) {
    try {
      fs.accessSync(candidate, fs.constants.X_OK);
      return candidate;
    } catch { /* try the next one */ }
  }
  return null;
}

/**
 * EchoMuseClient runs the sidecar and reads the state it reports.
 */
class EchoMuseClient {
  constructor({ binary, stateFile, levelFile, configFile, credentialsDir, deviceId, board, controllerUrl, pair, mic, speaker, runtimeDirectory, onState, onLog }) {
    this.binary = binary;
    this.stateFile = stateFile;
    this.levelFile = levelFile || `${stateFile}.level`;
    this.configFile = configFile;
    this.credentialsDir = credentialsDir;
    // The directory the live state is in. The device program is told
    // about it so that it writes the state where this reads it.
    this.runtimeDirectory = runtimeDirectory;
    this.deviceId = deviceId;
    this.board = board;
    this.controllerUrl = controllerUrl;
    this.pair = Boolean(pair);
    this.mic = mic || "";
    this.speaker = speaker || "";
    this.onState = onState || (() => {});
    this.onLog = onLog || (() => {});
    this.child = null;
    this.timer = null;
    this.state = { ...DEFAULT_STATE };
    this.lastRead = "";
  }

  /**
   * Starts the sidecar, if there is one.
   *
   * A missing sidecar is not a failure: the app still works as a dashboard,
   * it just cannot be an EchoMuse device.
   *
   * @returns {boolean} whether the sidecar was started
   */
  start() {
    if (!this.binary) {
      this.onLog("the EchoMuse sidecar is not installed");
      return false;
    }
    if (this.child) return true;

    const args = [
      "--device-id", this.deviceId,
      "--board", this.board,
      "--credentials-dir", this.credentialsDir,
      "--state-file", this.stateFile,
      "--level-file", this.levelFile,
    ];
    if (this.configFile) args.push("--config-file", this.configFile);
    if (this.controllerUrl) args.push("--controller", this.controllerUrl);
    // Pairing asks the controller to admit the device, which puts the
    // request in front of an administrator in the dashboard.
    if (this.pair) args.push("--pair");
    // The audio devices are named here as well as in the configuration, so
    // the device opens the right ones before the configuration is read.
    if (this.mic) args.push("--mic", this.mic);
    if (this.speaker) args.push("--speaker", this.speaker);

    // The spawn is guarded, because a path that is not executable throws
    // here rather than arriving as an error event, and a throw in here
    // takes the whole application down for a sidecar that is only an
    // extra.
    try {
      // The environment is passed on with the runtime directory named in
      // it, so the device program puts its state where the application
      // looks for it. The two have to agree: one writes the state and
      // the other reads it.
      this.child = spawn(this.binary, args, {
        stdio: ["ignore", "ignore", "pipe"],
        env: { ...process.env, RUNTIME_DIRECTORY: this.runtimeDirectory || "" },
      });
    } catch (error) {
      this.child = null;
      this.onLog(`the EchoMuse sidecar could not be started: ${error.message}`);
      return false;
    }
    this.child.stderr.on("data", (chunk) => this.onLog(String(chunk).trim()));
    this.child.on("exit", (code) => {
      this.child = null;
      this.onLog(`the EchoMuse sidecar stopped with code ${code}`);
    });
    this.child.on("error", (error) => {
      this.child = null;
      this.onLog(`the EchoMuse sidecar could not be started: ${error.message}`);
    });

    this.timer = setInterval(() => this.read(), POLL_INTERVAL_MS);
    return true;
  }

  /**
   * Reads the state file, if it has changed.
   *
   * The live microphone level is read from a file of its own. It changes
   * on every frame of audio, so it cannot be part of the state: a state
   * file that changed ten times a second would be written ten times a
   * second, and that wears the flash out on a device whose storage is
   * eMMC.
   */
  async read() {
    await this.readLevel();
    let raw;
    try {
      raw = await fsp.readFile(this.stateFile, "utf8");
    } catch {
      return;
    }
    if (raw === this.lastRead) return;
    this.lastRead = raw;
    let parsed;
    try {
      parsed = JSON.parse(raw);
    } catch {
      return;
    }
    this.state = { ...DEFAULT_STATE, ...parsed };
    this.onState(this.state);
  }

  /**
   * Reads the live microphone level.
   *
   * It is a small file beside the state, written a few times a second by
   * the device, and it carries the level and nothing else.
   */
  async readLevel() {
    let raw;
    try {
      raw = await fsp.readFile(this.levelFile, "utf8");
    } catch {
      return;
    }
    const level = Number(raw.trim());
    if (!Number.isFinite(level)) return;
    if (level === this.state.level) return;
    this.state.level = level;
    this.onState(this.state);
  }

  /**
   * Sends the configuration to the sidecar.
   *
   * The sidecar writes it to a file the controller reads, so a configuration
   * change survives a restart of either.
   *
   * @param {object} config the configuration
   */
  async sendConfig(config) {
    if (!this.configFile) return;
    try {
      const temporary = `${this.configFile}.tmp`;
      await fsp.writeFile(temporary, JSON.stringify(config, null, 2), { mode: 0o600 });
      await fsp.rename(temporary, this.configFile);
    } catch (error) {
      this.onLog(`the EchoMuse configuration could not be written: ${error.message}`);
    }
  }

  /**
   * Stops the sidecar, and waits for it to go.
   *
   * The wait is the point. The sidecar holds the microphone and the
   * speaker, and it releases them when it exits — so a new one started
   * before the old one has gone finds the sound card busy and cannot
   * open it. The controller then asks for the microphone and is told it
   * is busy, over and over, and the device never hears anything.
   *
   * The wait is bounded, because a sidecar that will not go must not
   * stop the application from starting its replacement.
   *
   * @returns {Promise<void>} resolved when the sidecar has gone
   */
  async stop() {
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
    const child = this.child;
    if (!child) return;
    this.child = null;

    await new Promise((resolve) => {
      let settled = false;
      const done = () => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        resolve();
      };
      // A sidecar that will not stop is killed. It is the last thing
      // tried, because a kill does not give it the chance to release the
      // sound card.
      const timer = setTimeout(() => {
        try { child.kill("SIGKILL"); } catch { /* it has already gone */ }
        done();
      }, STOP_TIMEOUT_MS);
      child.once("exit", done);
      child.once("error", done);
      try {
        child.kill("SIGTERM");
      } catch {
        done();
      }
    });
  }
}

module.exports = { EchoMuseClient, findSidecar, DEFAULT_STATE, BINARY_NAME };
