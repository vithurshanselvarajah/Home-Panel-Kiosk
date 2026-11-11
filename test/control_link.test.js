const test = require("node:test");
const assert = require("node:assert/strict");
const net = require("node:net");
const os = require("node:os");
const path = require("node:path");
const fs = require("node:fs");
const { ControlClient } = require("../src/control");

// The link to Home Assistant.
//
// The drawer shows whether the panel is linked to Home Assistant. The
// panel cannot tell: the socket between the panel and the device
// program is on this machine and is up whatever Home Assistant is
// doing, so the device program has to say.

function socketPath() {
  return path.join(os.tmpdir(), `homepanel-link-${process.pid}-${Date.now()}.sock`);
}

test("the client is told when the link is up and when it goes down", async () => {
  const socket = socketPath();
  const server = net.createServer((conn) => {
    conn.write(`${JSON.stringify({ action: "link", connected: true })}\n`);
    setTimeout(() => conn.write(`${JSON.stringify({ action: "link", connected: false })}\n`), 100);
  });
  await new Promise((resolve) => server.listen(socket, resolve));

  const links = [];
  const client = new ControlClient({ socket, onLink: (connected) => links.push(connected) });
  client.start();

  await waitFor(() => links.length >= 2);
  assert.deepEqual(links, [true, false]);

  client.stop();
  server.close();
  fs.rmSync(socket, { force: true });
});

test("a link message is not treated as a command", async () => {
  const socket = socketPath();
  const server = net.createServer((conn) => {
    conn.write(`${JSON.stringify({ action: "link", connected: true })}\n`);
  });
  await new Promise((resolve) => server.listen(socket, resolve));

  const commands = [];
  const links = [];
  const client = new ControlClient({
    socket,
    onCommand: (command) => commands.push(command),
    onLink: (connected) => links.push(connected),
  });
  client.start();

  await waitFor(() => links.length >= 1);
  assert.deepEqual(links, [true]);
  assert.equal(commands.length, 0, "the link message must not arrive as a command");

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
