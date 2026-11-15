// The device program.
//
// The panel is a voice satellite to Home Assistant, and the work of being
// one lives in a sidecar: a separate process that owns the connection to
// Home Assistant, the microphone, the speaker and the wake word. The
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

// How often the state file is read when it cannot be watched.
//
// The sidecar writes it when the state changes, so this is only how
// quickly a change is noticed. The watch notices it at once, so the
// poll is the backstop for a file system that does not report changes,
// and it is slow because it is only a backstop.
//
// The level is a different matter: it moves constantly and the panel
// draws it as a meter, so it is read at its own rate.
const POLL_INTERVAL_MS = 1000;

// How often the level is read.
//
// The level moves with every block of audio and the panel draws it as a
// meter, so it is read often. It is a file of a few bytes in memory,
// and reading it is cheap.
const LEVEL_INTERVAL_MS = 100;

// How long to wait after a change before reading the file.
//
// The sidecar writes the file atomically: it writes a temporary file and
// renames it over the one being read. A watch fires on the rename, and
// by then the file is whole. The wait is for the case where the watch
// fires on the temporary file instead, which would be a file that is
// not the state.
const WATCH_SETTLE_MS = 20;

// Where the sidecar lives. It is beside the Electron binary when the app is
// packaged, and in the repository's vendor directory when it is not.
const BINARY_NAME = "home-panel-device";

// How long the sidecar is given to stop before it is killed.
//
// The sidecar releases the microphone and the speaker when it exits, so
// the replacement needs the old one to have gone. The wait is bounded so
// a sidecar that hangs does not stop the application from starting.
const STOP_TIMEOUT_MS = 3000;

// How long to wait before the device program is started again.
//
// The wait is longer each time it fails, so a device program that
// cannot start is not started over and over.
const RESTART_FIRST_WAIT_MS = 1000;
const RESTART_MAX_WAIT_MS = 30_000;

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
  transcript: "",
  spoken: "",
  spoken_progress: 0,
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
  // The music. It is what the music server says is playing, and the
  // transport is what the server says it will take.
  music_state: "listening",
  music_paired: 0,
  music_buffered: 0,
  music_synced: false,
  music_title: "",
  music_artist: "",
  music_album: "",
  music_artwork: "",
  music_position: 0,
  music_duration: 0,
  music_playing: false,
  music_transport: [],
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
    path.join(appPath, "vendor", `home-panel-device-linux-${process.arch === "x64" ? "amd64" : process.arch}`),
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
 * DeviceClient runs the sidecar and reads the state it reports.
 */
class DeviceClient {
  constructor({ binary, stateFile, levelFile, configFile, settingsFile, dataDirectory, deviceId, board, mic, speaker, runtimeDirectory, controlSocket, esphomeKey, esphomeName, onState, onLog }) {
    this.binary = binary;
    this.stateFile = stateFile;
    this.levelFile = levelFile || `${stateFile}.level`;
    this.configFile = configFile;
    this.settingsFile = settingsFile;
    // The data directory is where the device program keeps the things it
    // has to keep: the music player's identity and its pairing records.
    //
    // It is on the disk, unlike the live state, because the music
    // player's identity has to survive a restart — a player that forgets
    // its identity is a new player, and the server has to pair with it
    // again.
    this.dataDirectory = dataDirectory;
    // The directory the live state is in. The device program is told
    // about it so that it writes the state where this reads it.
    this.runtimeDirectory = runtimeDirectory;
    this.deviceId = deviceId;
    this.board = board;
    this.mic = mic || "";
    this.speaker = speaker || "";
    // The control socket is where the panel talks to this program.
    this.controlSocket = controlSocket || "";
    // The Home Assistant connection is made here, so the key and the name
    // the panel is known by come from the panel.
    this.esphomeKey = esphomeKey || "";
    this.esphomeName = esphomeName || "";
    this.onState = onState || (() => {});
    this.onLog = onLog || (() => {});
    this.child = null;
    this.timer = null;
    // The pipes the children's errors came through. Every one of them
    // is closed when the panel stops.
    this.pipes = new Set();
    this.state = { ...DEFAULT_STATE };
    this.lastRead = "";
  }

  /**
   * Starts the sidecar, if there is one.
   *
   * A missing sidecar is not a failure: the app still works as a dashboard,
   * it just cannot be a voice satellite.
   *
   * @returns {boolean} whether the sidecar was started
   */
  start() {
    if (!this.binary) {
      this.onLog("the device program is not installed");
      return false;
    }
    if (this.child) return true;

    const args = [
      "--device-id", this.deviceId,
      "--board", this.board,
      "--credentials-dir", this.dataDirectory,
      "--state-file", this.stateFile,
      "--level-file", this.levelFile,
    ];
    if (this.configFile) args.push("--config-file", this.configFile);
    if (this.settingsFile) args.push("--settings-file", this.settingsFile);
    // The audio devices are named here as well as in the configuration, so
    // the device opens the right ones before the configuration is read.
    if (this.mic) args.push("--mic", this.mic);
    if (this.speaker) args.push("--speaker", this.speaker);
    // The control plane: the socket the panel talks over and the Home
    // Assistant connection.
    if (this.controlSocket) args.push("--control-socket", this.controlSocket);
    if (this.esphomeKey) args.push("--esphome-key", this.esphomeKey);
    if (this.esphomeName) args.push("--esphome-name", this.esphomeName);

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
        env: {
          ...process.env,
          RUNTIME_DIRECTORY: this.runtimeDirectory || "",
          ...this.libraryPath(),
        },
      });
    } catch (error) {
      this.child = null;
      this.onLog(`the device program could not be started: ${error.message}`);
      return false;
    }
    // The pipe the child's errors come through is kept, so that it can
    // be closed when the child goes.
    //
    // It is kept as a local, because the pipe belongs to this child and
    // not to the panel: a child that is started again has its own pipe,
    // and the panel's would be the wrong one to close.
    const stderr = this.child.stderr;
    this.stderr = stderr;
    // Every pipe is remembered, so that they can all be closed when the
    // panel stops. A pipe that is left open is a panel that cannot
    // shut down.
    this.pipes.add(stderr);
    stderr.on("data", (chunk) => this.onLog(String(chunk).trim()));
    this.child.on("exit", (code) => {
      this.child = null;
      // The pipe the child's errors came through is closed.
      //
      // A pipe whose other end has gone stays open, and an open pipe is
      // a panel that cannot shut down: the process waits for the pipe
      // to close and the pipe never does.
      stderr.destroy();
      this.pipes.delete(stderr);
      if (this.stderr === stderr) this.stderr = null;
      this.onLog(`the device program stopped with code ${code}`);
      this.restart();
    });
    this.child.on("error", (error) => {
      this.child = null;
      this.onLog(`the device program could not be started: ${error.message}`);
      this.restart();
    });

    // The state file is watched rather than polled.
    //
    // Polling means reading the file four times a second for as long as
    // the panel is running, and most of those reads find nothing new.
    // The watch is told when the file changes, so the file is read when
    // there is something to read.
    //
    // The watch is not trusted on its own: a file system that does not
    // report changes would leave the panel showing a state that is not
    // the device's, so the poll goes on beside it as a backstop.
    this.watch();
    this.timer = setInterval(() => this.read(), POLL_INTERVAL_MS);
    this.levelTimer = setInterval(() => this.readLevel(), LEVEL_INTERVAL_MS);
    return true;
  }

  /**
   * Watches the state file for changes.
   *
   * The watch is best effort: a file system that cannot be watched
   * leaves the poll to do the work, and a panel that cannot watch is
   * still a panel that works.
   */
  watch() {
    try {
      this.watcher = fs.watch(path.dirname(this.stateFile), (_event, name) => {
        if (name && name !== path.basename(this.stateFile)) return;
        if (this.settle) clearTimeout(this.settle);
        this.settle = setTimeout(() => this.read(), WATCH_SETTLE_MS);
      });
      // The watch does not keep the panel alive: the panel is kept alive
      // by its window.
      this.watcher.unref?.();
    } catch {
      // The poll is the backstop, so a file system that cannot be
      // watched is not a failure.
    }
  }

  /**
   * Starts the device program again after it has stopped.
   *
   * The device program is the panel's voice: it owns the microphone, the
   * speaker, the wake word and the connection to Home Assistant. A
   * device program that has stopped is a panel that cannot hear and
   * cannot be heard, and a kiosk that is on a wall for a year should
   * not need a person to visit it.
   *
   * The wait is longer each time, so a device program that will not
   * start is not started over and over: the panel says what is wrong
   * and waits.
   */
  restart() {
    if (this.stopping) return;
    if (this.restartTimer) return;
    const wait = Math.min(RESTART_MAX_WAIT_MS, (this.restartWait || 0) * 2 || RESTART_FIRST_WAIT_MS);
    this.restartWait = wait;
    this.restartTimer = setTimeout(() => {
      this.restartTimer = null;
      if (this.stopping) return;
      this.onLog("the device program is being started again");
      this.start();
    }, wait);
    this.restartTimer.unref?.();
  }

  /**
   * The library path the device program is started with.
   *
   * The device program is linked against the ONNX Runtime, which it
   * looks for beside itself. In the package the runtime is beside the
   * binary, so nothing is needed. In a development run the binary is in
   * the repository's vendor directory and the runtime is beside it too,
   * but the loader does not look there for a development run, so the
   * directory is named here.
   *
   * @returns {object} the environment to add
   */
  libraryPath() {
    // The runtime is beside the binary in the package, and in the
    // repository's own directory in a development run. The loader finds
    // a library by its soname, so the directory has to hold a file with
    // that name.
    const architecture = process.arch === "x64" ? "amd64" : process.arch;
    const directories = [
      path.dirname(this.binary || ""),
      path.join(path.dirname(this.binary || ""), architecture),
    ];
    const found = directories.find((directory) => {
      try {
        fs.accessSync(path.join(directory, "libonnxruntime.so.1"), fs.constants.R_OK);
        return true;
      } catch {
        return false;
      }
    });
    if (!found) return {};

    const current = process.env.LD_LIBRARY_PATH;
    return {
      LD_LIBRARY_PATH: current ? `${found}:${current}` : found,
    };
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
   * The sidecar writes it to a file it reads itself, so a configuration
   * change survives a restart of either.
   *
   * @param {object} config the configuration
   */
  async sendConfig(config) {
    if (!this.configFile) return;
    try {
      // The file is merged, not replaced.
      //
      // Some of the settings in it belong to the device program: the
      // wake word's sensitivity, the ducking and the level. The panel
      // does not know about them, and replacing the file would take
      // them out of it — so a setting that was chosen would be lost the
      // next time the panel wrote the file, which is on every start.
      //
      // So what is written is put into what is there, and what the
      // panel does not know about is left alone.
      let existing = {};
      try {
        existing = JSON.parse(await fsp.readFile(this.configFile, "utf8"));
      } catch { /* there is no file yet, or it is not readable */ }
      const merged = { ...existing, ...config };
      const temporary = `${this.configFile}.tmp`;
      await fsp.writeFile(temporary, JSON.stringify(merged, null, 2), { mode: 0o600 });
      await fsp.rename(temporary, this.configFile);
    } catch (error) {
      this.onLog(`the device configuration could not be written: ${error.message}`);
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
    // The device program is not started again once the panel is going
    // away.
    this.stopping = true;
    if (this.restartTimer) clearTimeout(this.restartTimer);
    this.restartTimer = null;
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
    if (this.levelTimer) clearInterval(this.levelTimer);
    this.levelTimer = null;
    // The watch is closed with the poll, so that a stopped device
    // leaves nothing behind that would read a file that is not there.
    if (this.watcher) this.watcher.close();
    this.watcher = null;
    if (this.settle) clearTimeout(this.settle);
    this.settle = null;
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

    // Every pipe the children's errors came through is closed. A pipe
    // whose other end has gone stays open, and an open pipe is a panel
    // that cannot shut down.
    for (const pipe of this.pipes) pipe.destroy();
    this.pipes.clear();
    this.stderr = null;
  }
}

module.exports = { DeviceClient, findSidecar, DEFAULT_STATE, BINARY_NAME };
