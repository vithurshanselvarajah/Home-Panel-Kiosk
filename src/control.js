// The control plane.
//
// The panel and the device program are two processes. The device
// program owns the link to Home Assistant, and the panel talks to it
// over a socket.
//
// The socket is in memory, because it carries what is happening right
// now rather than anything that is worth keeping. The panel is a
// display: it redraws when it is told something changed, and a socket
// tells it the moment it changes rather than when it happens to look.

const net = require("node:net");
const fs = require("node:fs");
const path = require("node:path");

// How long the connection is given before it is tried again.
const RETRY_INTERVAL_MS = 2000;

// The state the panel reports.
//
// The panel is the thing being displayed, so it is the one that knows
// what it is doing. The device program turns this into the entities
// Home Assistant sees.
const DEFAULT_STATE = {
  mode: "dashboard",
  displayIsOff: false,
  keyboardVisible: false,
  sidePanelOpen: false,
  displayMode: "fullscreen",
  brightness: 60,
  volume: 60,
};

/**
 * ControlClient talks to the device program over the control socket.
 */
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
  }

  /**
   * Starts the connection.
   *
   * The socket may not be there yet: the device program makes it when
   * it starts, and the two are started together. So the connection is
   * tried again until it works.
   */
  start() {
    if (!this.socket) {
      this.onLog("the control socket has no name");
      return false;
    }
    this.connect();
    this.timer = setInterval(() => this.connect(), RETRY_INTERVAL_MS);
    return true;
  }

  /**
   * Connects to the socket, if it is not connected already.
   */
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
      // The state is sent as soon as the connection is there.
      //
      // The state is sent when it changes, and the connection is made
      // after the first state has been sent — so without this the device
      // program never hears what the panel is doing until something
      // else changes.
      //
      // The owner is asked for it rather than the cached copy being
      // sent, because the cached copy is what was last sent and the
      // panel's own state is what is current.
      if (this.onConnect) this.onConnect();
    });
    this.connection.on("data", (chunk) => this.receive(chunk));
    this.connection.on("close", () => {
      this.connected = false;
      this.connection = null;
      this.onStatus("offline");
    });
    this.connection.on("error", (error) => {
      // A socket that is not there yet is expected, so it is not
      // reported as a failure. The next try picks it up.
      this.onStatus("offline");
      this.connection = null;
    });
  }

  /**
   * Reads what the device program sent.
   *
   * The messages are newline separated JSON. They are the commands
   * Home Assistant asked for.
   */
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
      // The link message says whether Home Assistant is talking to the
      // device program. The rest are commands.
      if (parsed && parsed.action === "link") {
        this.onLink(Boolean(parsed.connected));
        continue;
      }
      this.onCommand(parsed);
    }
  }

  /**
   * Sends the panel's state to the device program.
   *
   * The state goes over as one message. It is sent when it changes and
   * not on a timer, because the device program turns it into the
   * entities Home Assistant sees and there is no reason to publish a
   * state that has not changed.
   *
   * @param {object} state the state of the panel
   * @returns {boolean} whether it was sent
   */
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

  /**
   * Asks the device program to drive the music transport.
   *
   * The transport belongs to the music server and the panel has the
   * buttons, so the request goes over the socket and the device program
   * passes it on.
   *
   * @param {object} command the command
   * @returns {boolean} whether it was sent
   */
  /**
   * Asks the device program to mute the microphone.
   *
   * The mute is the microphone's, and the device program owns the
   * microphone, so the panel asks for it rather than doing it itself.
   *
   * @param {boolean} muted whether the microphone is muted
   * @returns {boolean} whether the request was sent
   */
  sendMute(muted) {
    if (!this.connection || !this.connected) return false;
    try {
      this.connection.write(`${JSON.stringify({ action: "mute", muted: Boolean(muted) })}\n`);
      return true;
    } catch (error) {
      this.onLog(`the mute could not be sent: ${error.message}`);
      return false;
    }
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

  /**
   * Stops the connection.
   */
  stop() {
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
    if (this.connection) {
      try {
        this.connection.destroy();
      } catch { /* it has already gone */ }
      this.connection = null;
    }
    this.connected = false;
  }
}

/**
 * Finds the control socket.
 *
 * The socket is in the runtime directory, which is in memory. The
 * application and the device program are told about it through
 * RUNTIME_DIRECTORY, so the two cannot disagree about where it is.
 *
 * @param {string} runtimeDirectory the directory the runtime lives in
 * @returns {string|null} the path to the socket, or null when there is none
 */
function findControlSocket(runtimeDirectory) {
  // The socket is in the device's own directory, beside the live state.
  const base = runtimeDirectory || "/dev/shm/homepanel";
  const candidates = [
    path.join(base, "control.sock"),
    path.join(path.dirname(base), "control.sock"),
  ];
  for (const candidate of candidates) {
    try {
      fs.accessSync(candidate, fs.constants.F_OK);
      return candidate;
    } catch { /* try the next one */ }
  }
  // The socket may not be there yet, so the first candidate is used
  // anyway: the connection is retried until it appears.
  return candidates[0];
}

module.exports = { ControlClient, findControlSocket, DEFAULT_STATE };
