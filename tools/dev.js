// Runs the app against a Home Assistant server.
//
//   npm run dev              the app, with the voice switched on
//   npm run dev -- --no-voice
//                            the app, without the voice
//
// The app is a voice satellite to Home Assistant. It finds Home Assistant
// on the network the same way any ESPHome device does, so there is
// nothing to point it at and no controller to start.
//
// The voice is off by default, so a development run turns it on: the
// settings are the panel's own, and a development run should not need the
// settings panel open to get there.

const { spawn } = require("node:child_process");
const path = require("node:path");

const root = path.join(__dirname, "..");
const args = process.argv.slice(2);
const useVoice = !args.includes("--no-voice");

const children = [];
function cleanup() {
  for (const child of children) {
    try { child.kill(); } catch { /* already gone */ }
  }
}
process.on("exit", cleanup);
process.on("SIGINT", () => { cleanup(); process.exit(0); });
process.on("SIGTERM", () => { cleanup(); process.exit(0); });

console.log("dev: starting the app");
const app = spawn("npx", ["electron", ".", "--dev"], {
  cwd: root,
  stdio: "inherit",
  env: {
    ...process.env,
    // A development run turns the voice on, because the panel is a voice
    // satellite and that is what is being worked on.
    HOME_PANEL_VOICE: useVoice ? "1" : "0",
  },
});
children.push(app);
app.on("exit", (code) => { cleanup(); process.exit(code || 0); });
