const test = require("node:test");
const assert = require("node:assert/strict");
const net = require("node:net");
const os = require("node:os");
const path = require("node:path");
const fs = require("node:fs");
const { ControlClient } = require("../src/control");

// The control plane is a socket between the panel and the device
// program. These tests drive the panel's end of it.

function socketPath() {
  return path.join(os.tmpdir(), `homepanel-control-${process.pid}-${Date.now()}.sock`);
}

test("the client sends the state and the peer reads it", async () => {
  const socket = socketPath();
  const received = [];
  const server = net.createServer((conn) => {
    conn.on("data", (chunk) => {
      for (const line of chunk.toString("utf8").split("\n")) {
        if (line.trim()) received.push(JSON.parse(line));
      }
    });
  });
  await new Promise((resolve) => server.listen(socket, resolve));

  const client = new ControlClient({ socket });
  client.start();
  await waitFor(() => client.connected);

  client.sendState({ mode: "slideshow", displayIsOff: true, brightness: 42 });
  await waitFor(() => received.length > 0);

  assert.equal(received[0].action, "state");
  assert.equal(received[0].value.mode, "slideshow");
  assert.equal(received[0].value.displayIsOff, true);
  assert.equal(received[0].value.brightness, 42);
  // The fields that were not given keep their defaults.
  assert.equal(received[0].value.displayMode, "fullscreen");

  client.stop();
  server.close();
  fs.rmSync(socket, { force: true });
});

test("the client gives a command to the peer", async () => {
  const socket = socketPath();
  const received = [];
  let peer = null;
  const server = net.createServer((conn) => {
    peer = conn;
    conn.on("data", (chunk) => {
      for (const line of chunk.toString("utf8").split("\n")) {
        if (line.trim()) received.push(JSON.parse(line));
      }
    });
  });
  await new Promise((resolve) => server.listen(socket, resolve));

  const commands = [];
  const client = new ControlClient({
    socket,
    onCommand: (command) => commands.push(command),
  });
  client.start();
  await waitFor(() => client.connected);

  // The peer sends a command.
  await waitFor(() => peer !== null);
  peer.write(`${JSON.stringify({ action: "screen_off" })}\n`);
  await waitFor(() => commands.length > 0);

  assert.equal(commands[0].action, "screen_off");

  client.stop();
  server.close();
  fs.rmSync(socket, { force: true });
});

test("the client reports offline when the socket is not there", async () => {
  const status = [];
  const client = new ControlClient({
    socket: path.join(os.tmpdir(), "homepanel-control-not-there.sock"),
    onStatus: (value) => status.push(value),
  });
  client.start();
  await waitFor(() => status.includes("offline"));
  assert.equal(status[0], "offline");
  client.stop();
});

test("the client keeps trying when the socket is not there yet", async () => {
  const socket = socketPath();
  const status = [];
  const client = new ControlClient({
    socket,
    onStatus: (value) => status.push(value),
  });
  client.start();
  await waitFor(() => status.includes("offline"));

  // The device program starts and makes the socket.
  const server = net.createServer(() => {});
  await new Promise((resolve) => server.listen(socket, resolve));

  await waitFor(() => client.connected, 8000);
  assert.equal(client.connected, true);

  client.stop();
  server.close();
  fs.rmSync(socket, { force: true });
});

test("a line that is not JSON does not take the connection down", async () => {
  const socket = socketPath();
  const commands = [];
  const server = net.createServer((conn) => {
    conn.write("this is not json\n");
    conn.write(`${JSON.stringify({ action: "wake" })}\n`);
  });
  await new Promise((resolve) => server.listen(socket, resolve));

  const client = new ControlClient({
    socket,
    onCommand: (command) => commands.push(command),
  });
  client.start();
  await waitFor(() => commands.length > 0);

  assert.equal(commands[0].action, "wake");

  client.stop();
  server.close();
  fs.rmSync(socket, { force: true });
});

test("the client sends the state and the peer sees it as JSON", async () => {
  const socket = socketPath();
  const received = [];
  const server = net.createServer((conn) => {
    conn.on("data", (chunk) => {
      for (const line of chunk.toString("utf8").split("\n")) {
        if (line.trim()) received.push(JSON.parse(line));
      }
    });
  });
  await new Promise((resolve) => server.listen(socket, resolve));

  const client = new ControlClient({ socket });
  client.start();
  await waitFor(() => client.connected);

  client.sendState({ mode: "slideshow", displayIsOff: true });
  await waitFor(() => received.length > 0);

  // The device program reads the state as one message with the state
  // under a value.
  assert.equal(received[0].action, "state");
  assert.equal(received[0].value.mode, "slideshow");
  assert.equal(received[0].value.displayIsOff, true);

  client.stop();
  server.close();
  fs.rmSync(socket, { force: true });
});

async function waitFor(predicate, timeout = 5000) {
  const started = Date.now();
  while (Date.now() - started < timeout) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  throw new Error("the wait ran out");
}
