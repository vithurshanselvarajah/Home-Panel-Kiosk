

const net = require("node:net");
const fs = require("node:fs");
const path = require("node:path");

const RETRY_INTERVAL_MS = 2000;

const DEFAULT_STATE = {
  mode: "dashboard",
  displayIsOff: false,
  keyboardVisible: false,
  sidePanelOpen: false,
  displayMode: "fullscreen",
  brightness: 60,
  volume: 60,
};

class ControlClient {
  constructor({ socket, onState, onCommand, onStatus, onLink, onLog, onConnect }) {
    this.socket = socket;
    this.onState = onState || (() => {});
    this.onCommand = onCommand || (() => {});
    this.onStatus = onStatus || (() => {});
    this.onLink = onLink || (() => {});
    this.onLog = onLog || (() => {});
    this.onConnect = onConnect || null;
    this.connection = null;
    this.timer = null;
    this.buffer = "";
    this.state = { ...DEFAULT_STATE };
    this.connected = false;
    this.requestSequence = 0;
    this.pendingSendspinTokens = new Map();
  }

  start() {
    if (!this.socket) {
      this.onLog("the control socket has no name");
      return false;
    }
    this.connect();
    this.timer = setInterval(() => this.connect(), RETRY_INTERVAL_MS);
    return true;
  }

  connect() {
    if (this.connection) return;
    try {
      this.connection = net.createConnection(this.socket);
    } catch (error) {
      this.onStatus("offline");
      return;
    }
    this.connection.on("connect", () => {
      this.connected = true;
      this.onStatus("online");
      this.onLog("the device program was reached");

      if (this.onConnect) this.onConnect();
    });
    this.connection.on("data", (chunk) => this.receive(chunk));
    this.connection.on("close", () => {
      this.connected = false;
      this.connection = null;
      this.rejectPendingSendspinTokens(new Error("The device program disconnected."));
      this.onStatus("offline");
    });
    this.connection.on("error", (error) => {

      this.onStatus("offline");
      this.connection = null;
      this.rejectPendingSendspinTokens(error);
    });
  }

  receive(chunk) {
    this.buffer += chunk.toString("utf8");
    let index;
    while ((index = this.buffer.indexOf("\n")) >= 0) {
      const line = this.buffer.slice(0, index).trim();
      this.buffer = this.buffer.slice(index + 1);
      if (!line) continue;
      let parsed;
      try {
        parsed = JSON.parse(line);
      } catch {
        continue;
      }

      if (parsed && parsed.action === "link") {
        this.onLink(Boolean(parsed.connected));
        continue;
      }
      if (parsed && parsed.action === "sendspin_token_response") {
        const pending = this.pendingSendspinTokens.get(parsed.requestId);
        if (pending) {
          clearTimeout(pending.timer);
          this.pendingSendspinTokens.delete(parsed.requestId);
          if (parsed.error) pending.reject(new Error(parsed.error));
          else pending.resolve(parsed.sendspinToken || "");
        }
        continue;
      }
      this.onCommand(parsed);
    }
  }

  sendState(state) {
    if (!this.connection || !this.connected) return false;
    this.state = { ...DEFAULT_STATE, ...state };
    try {
      this.connection.write(`${JSON.stringify({ action: "state", value: this.state })}\n`);
      return true;
    } catch (error) {
      this.onLog(`the state could not be sent: ${error.message}`);
      return false;
    }
  }

  sendCameraPassword(password = "") {
    if (!this.connection || !this.connected) return false;
    try {
      this.connection.write(`${JSON.stringify({ action: "camera_password", cameraPassword: password })}\n`);
      return true;
    } catch (error) {
      this.onLog(`the camera password could not be sent: ${error.message}`);
      return false;
    }
  }

  requestSendspinToken() {
    if (!this.connection || !this.connected) return Promise.reject(new Error("The device program is not connected."));
    const requestId = `sendspin-${++this.requestSequence}`;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pendingSendspinTokens.delete(requestId);
        reject(new Error("Timed out while reading the Sendspin pairing token."));
      }, 5000);
      this.pendingSendspinTokens.set(requestId, { resolve, reject, timer });
      try {
        this.connection.write(`${JSON.stringify({ action: "sendspin_token_request", requestId })}\n`);
      } catch (error) {
        clearTimeout(timer);
        this.pendingSendspinTokens.delete(requestId);
        reject(error);
      }
    });
  }

  rejectPendingSendspinTokens(error) {
    for (const pending of this.pendingSendspinTokens.values()) {
      clearTimeout(pending.timer);
      pending.reject(error);
    }
    this.pendingSendspinTokens.clear();
  }

  sendTransport(command) {
    if (!this.connection || !this.connected) return false;
    try {
      this.connection.write(`${JSON.stringify({ action: "transport", command })}\n`);
      return true;
    } catch (error) {
      this.onLog(`the transport command could not be sent: ${error.message}`);
      return false;
    }
  }

  sendVolume(percent) {
    if (!this.connection || !this.connected) return false;
    try {
      const level = Math.max(0, Math.min(100, Math.round(Number(percent))));
      this.connection.write(`${JSON.stringify({ action: "volume", volume: level })}\n`);
      return true;
    } catch (error) {
      this.onLog(`the volume could not be sent: ${error.message}`);
      return false;
    }
  }

  stop() {
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
    for (const pending of this.pendingSendspinTokens.values()) {
      clearTimeout(pending.timer);
      pending.reject(new Error("The device connection closed."));
    }
    this.pendingSendspinTokens.clear();
    if (this.connection) {
      try {
        this.connection.destroy();
      } catch {  }
      this.connection = null;
    }
    this.connected = false;
  }
}

function findControlSocket(runtimeDirectory) {

  const base = runtimeDirectory || "/dev/shm/homepanel";
  const candidates = [
    path.join(base, "control.sock"),
    path.join(path.dirname(base), "control.sock"),
  ];
  for (const candidate of candidates) {
    try {
      fs.accessSync(candidate, fs.constants.F_OK);
      return candidate;
    } catch {  }
  }

  return candidates[0];
}

module.exports = { ControlClient, findControlSocket, DEFAULT_STATE };
