

const { spawn } = require("node:child_process");
const path = require("node:path");

const root = path.join(__dirname, "..");

const children = [];
function cleanup() {
  for (const child of children) {
    try { child.kill(); } catch {  }
  }
}
process.on("exit", cleanup);
process.on("SIGINT", () => { cleanup(); process.exit(0); });
process.on("SIGTERM", () => { cleanup(); process.exit(0); });

console.log("dev: starting the app");
const app = spawn("npx", ["electron", ".", "--dev"], {
  cwd: root,
  stdio: "inherit",
  env: process.env,
});
children.push(app);
app.on("exit", (code) => { cleanup(); process.exit(code || 0); });
