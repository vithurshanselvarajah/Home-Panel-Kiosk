const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const root = path.join(__dirname, "..");

function loadMain() {
  const source = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

  const app = {
    whenReady: () => new Promise(() => {}),
    on: () => {},
    quit: () => {},
    getPath: () => "/tmp/homepanel-test",
    getAppPath: () => root,
    requestSingleInstanceLock: () => true,
    setAppUserModelId: () => {},
    commandLine: { appendSwitch: () => {} },
    disableHardwareAcceleration: () => {},
    setSpellCheckerLanguages: () => {},
  };
  const ipcMain = { handle: () => {}, on: () => {} };
  const BrowserWindow = class {
    static getAllWindows() { return []; }
    static fromWebContents() { return null; }
  };
  const WebContentsView = class {
    constructor() { this.webContents = { send: () => {}, on: () => {}, setWindowOpenHandler: () => {}, insertCSS: () => {} }; }
  };
  const screen = { getPrimaryDisplay: () => ({ workArea: { x: 0, y: 0, width: 1920, height: 1080 } }) };
  const contextBridge = { exposeInMainWorld: () => {} };

  const session = {
    defaultSession: {
      setSpellCheckerLanguages: () => {},
      setSpellCheckerEnabled: () => {},
    },
  };
  const safeStorage = {
    isAsyncEncryptionAvailable: () => Promise.resolve(false),
    encryptStringAsync: () => Promise.resolve(Buffer.alloc(0)),
    decryptStringAsync: () => Promise.resolve(""),
  };
  const electron = {
    app,
    ipcMain,
    BrowserWindow,
    WebContentsView,
    screen,
    contextBridge,
    session,
    safeStorage,
  };

  const moduleRequire = (id) => {
    if (id === "electron") return electron;

    if (id.startsWith("node:")) return require(id);
    if (!id.startsWith(".")) return require(id);
    return require(path.join(root, "src", id));
  };

  const noopTimer = () => 0;
  const sandbox = {
    require: moduleRequire,
    module: { exports: {} },
    exports: {},
    console,
    process,
    __dirname: path.join(root, "src"),
    __filename: path.join(root, "src", "main.js"),
    Buffer,
    setTimeout: noopTimer,
    clearTimeout: noopTimer,
    setInterval: noopTimer,
    clearInterval: noopTimer,
    URL,
    URLSearchParams,
    app,
    ipcMain,
    BrowserWindow,
    WebContentsView,
    screen,
    contextBridge,
  };
  sandbox.global = sandbox;
  sandbox.globalThis = sandbox;

  vm.createContext(sandbox);
  vm.runInContext(source, sandbox, { filename: "main.js" });
  return sandbox;
}

test("the main process loads without a ReferenceError", () => {

  assert.doesNotThrow(() => loadMain());
});

test("the main process defines the helpers it uses", () => {
  const sandbox = loadMain();

  for (const name of ["runtimeDirectory", "runtimeBase", "isMemory"]) {
    assert.equal(typeof sandbox[name], "function", `${name} is not defined`);
  }
});

test("the main process reaches no undefined name", () => {
  const source = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

  const declared = new Set();
  for (const match of source.matchAll(/^(?:async )?function ([A-Za-z0-9_$]+)/gm)) declared.add(match[1]);
  for (const match of source.matchAll(/^(?:const|let|var) ([A-Za-z0-9_$]+)\s*=/gm)) declared.add(match[1]);

  for (const match of source.matchAll(/^const \{([^}]+)\} = require/gm)) {
    for (const name of match[1].split(",")) {
      const parts = name.trim().split(":");
      declared.add((parts[1] || parts[0]).trim());
    }
  }

  const words = new Set([
    "if", "else", "for", "while", "do", "switch", "case", "catch", "try",
    "return", "typeof", "new", "await", "async", "function", "delete",
    "void", "in", "of", "instanceof", "class", "super", "this", "throw",
    "yield", "default", "finally", "break", "continue",
  ]);

  const code = source
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/\/\/[^\n]*/g, "")
    .replace(/"(?:[^"\\\n]|\\.)*"/g, '""')
    .replace(/'(?:[^'\\\n]|\\.)*'/g, "''")
    .replace(/`(?:[^`\\]|\\.)*`/g, "``");

  const used = new Set();
  for (const match of code.matchAll(/(?<![.\w$])([a-z][A-Za-z0-9_$]*)\s*\(/g)) {
    if (!words.has(match[1])) used.add(match[1]);
  }

  for (const match of code.matchAll(/\b(?:const|let|var)\s+([A-Za-z0-9_$]+)\s*=/g)) declared.add(match[1]);
  for (const match of code.matchAll(/\bfunction\s+([A-Za-z0-9_$]+)\s*\(/g)) declared.add(match[1]);
  for (const match of code.matchAll(/\(([A-Za-z0-9_$,\s]*)\)\s*=>/g)) {
    for (const name of match[1].split(",")) {
      const clean = name.trim().split(/[=\s]/)[0];
      if (clean) declared.add(clean);
    }
  }

  const elsewhere = new Set([
    "require", "console", "process", "Buffer", "setTimeout", "clearTimeout",
    "setInterval", "clearInterval", "parseInt", "parseFloat", "String",
    "Number", "Boolean", "Array", "Object", "JSON", "Math", "Date", "Promise",
    "URL", "URLSearchParams", "structuredClone", "isNaN", "isFinite",
    "encodeURIComponent", "decodeURIComponent", "queueMicrotask",
  ]);
  for (const match of source.matchAll(/^const \{([^}]+)\} = require/gm)) {
    for (const name of match[1].split(",")) elsewhere.add(name.trim().split(":")[0]);
  }
  for (const match of source.matchAll(/\bfunction ([A-Za-z0-9_$]+)\(/g)) elsewhere.add(match[1]);

  const missing = [...used].filter((name) => !declared.has(name) && !elsewhere.has(name));
  assert.deepEqual(missing, [], `the main process uses names it does not define: ${missing.join(", ")}`);
});
