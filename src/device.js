

const { spawn } = require("node:child_process");
const fs = require("node:fs");
const fsp = require("node:fs/promises");
const path = require("node:path");

const POLL_INTERVAL_MS = 1000;

const LEVEL_INTERVAL_MS = 100;

const WATCH_SETTLE_MS = 20;

const BINARY_NAME = "home-panel-device";

const STOP_TIMEOUT_MS = 3000;

const RESTART_FIRST_WAIT_MS = 1000;
const RESTART_MAX_WAIT_MS = 30_000;

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

function findSidecar(appPath, resourcesPath, execPath) {
  const candidates = [

    path.join(path.dirname(execPath || ""), BINARY_NAME),

    path.join(path.dirname(resourcesPath), BINARY_NAME),
    path.join(resourcesPath, BINARY_NAME),
    path.join(path.dirname(appPath), BINARY_NAME),

    path.join(appPath, "vendor", `home-panel-device-linux-${process.arch === "x64" ? "amd64" : process.arch}`),
  ];
  for (const candidate of candidates) {
    try {
      fs.accessSync(candidate, fs.constants.X_OK);
      return candidate;
    } catch {  }
  }
  return null;
}

class DeviceClient {
  constructor({ binary, stateFile, levelFile, configFile, settingsFile, dataDirectory, deviceId, board, mic, speaker, runtimeDirectory, controlSocket, esphomeKey, esphomeName, onState, onLog }) {
    this.binary = binary;
    this.stateFile = stateFile;
    this.levelFile = levelFile || `${stateFile}.level`;
    this.configFile = configFile;
    this.settingsFile = settingsFile;

    this.dataDirectory = dataDirectory;

    this.runtimeDirectory = runtimeDirectory;
    this.deviceId = deviceId;
    this.board = board;
    this.mic = mic || "";
    this.speaker = speaker || "";

    this.controlSocket = controlSocket || "";

    this.esphomeKey = esphomeKey || "";
    this.esphomeName = esphomeName || "";
    this.onState = onState || (() => {});
    this.onLog = onLog || (() => {});
    this.child = null;
    this.timer = null;

    this.pipes = new Set();
    this.state = { ...DEFAULT_STATE };
    this.lastRead = "";
  }

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

    if (this.mic) args.push("--mic", this.mic);
    if (this.speaker) args.push("--speaker", this.speaker);

    if (this.controlSocket) args.push("--control-socket", this.controlSocket);
    if (this.esphomeKey) args.push("--esphome-key", this.esphomeKey);
    if (this.esphomeName) args.push("--esphome-name", this.esphomeName);

    try {

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

    const stderr = this.child.stderr;
    this.stderr = stderr;

    this.pipes.add(stderr);
    stderr.on("data", (chunk) => this.onLog(String(chunk).trim()));
    this.child.on("exit", (code) => {
      this.child = null;

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

    this.watch();
    this.timer = setInterval(() => this.read(), POLL_INTERVAL_MS);
    this.levelTimer = setInterval(() => this.readLevel(), LEVEL_INTERVAL_MS);
    return true;
  }

  watch() {
    try {
      this.watcher = fs.watch(path.dirname(this.stateFile), (_event, name) => {
        if (name && name !== path.basename(this.stateFile)) return;
        if (this.settle) clearTimeout(this.settle);
        this.settle = setTimeout(() => this.read(), WATCH_SETTLE_MS);
      });

      this.watcher.unref?.();
    } catch {

    }
  }

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

  libraryPath() {

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

  async sendConfig(config) {
    if (!this.configFile) return;
    try {

      let existing = {};
      try {
        existing = JSON.parse(await fsp.readFile(this.configFile, "utf8"));
      } catch {  }
      const merged = { ...existing, ...config };
      const temporary = `${this.configFile}.tmp`;
      await fsp.writeFile(temporary, JSON.stringify(merged, null, 2), { mode: 0o600 });
      await fsp.rename(temporary, this.configFile);
    } catch (error) {
      this.onLog(`the device configuration could not be written: ${error.message}`);
    }
  }

  async stop() {

    this.stopping = true;
    if (this.restartTimer) clearTimeout(this.restartTimer);
    this.restartTimer = null;
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
    if (this.levelTimer) clearInterval(this.levelTimer);
    this.levelTimer = null;

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

      const timer = setTimeout(() => {
        try { child.kill("SIGKILL"); } catch {  }
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

    for (const pipe of this.pipes) pipe.destroy();
    this.pipes.clear();
    this.stderr = null;
  }
}

module.exports = { DeviceClient, findSidecar, DEFAULT_STATE, BINARY_NAME };
