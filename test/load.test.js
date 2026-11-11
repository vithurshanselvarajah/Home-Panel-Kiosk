const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const root = path.join(__dirname, "..");

// The main process is loaded with Electron's globals stubbed out.
//
// It cannot simply be required: it starts a window, opens sockets and
// registers IPC handlers the moment it is loaded, and it needs Electron's
// own modules to do that. So the source is read and evaluated with
// stand-ins for the things it reaches for.
//
// The point of the test is not that the application runs — it is that
// every name it uses is defined. A function that is called but never
// declared is a ReferenceError the moment that line is reached, which in
// the main process is at load time: the application does not start, and
// nothing says why except a stack trace.
//
// It is a plain mistake and it is easy to make by accident — a rename
// that removes a helper takes the application with it — so it is worth
// a test.

/**
 * Reads the main process's source and evaluates it with Electron stubbed.
 *
 * @returns {object} the names the module defined
 */
function loadMain() {
  const source = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

  // The globals the module reaches for.
  // The ready handler starts the whole application — the window, the
  // sockets, the device program. The test is about the module loading,
  // not about the application starting, so the promise is left pending:
  // the handler never runs, and nothing is left behind.
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
  // The session is asked for the spellchecker's languages when the app
  // is ready. It is stubbed because it is asked for asynchronously, and
  // the test has finished by then.
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

  // The module requires its own files by a relative path, so the
  // require is given the module's own directory as its base.
  const moduleRequire = (id) => {
    if (id === "electron") return electron;
    // Node's own modules are not the application's.
    if (id.startsWith("node:")) return require(id);
    return require(path.join(root, "src", id));
  };

  // The timers are replaced with ones that do nothing.
  //
  // The module sets timers as it loads — the idle timers, the retry
  // timer — and a real one keeps the process alive after the test has
  // finished, which makes the test runner wait for it.
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
  // The module is evaluated at load, so a name it uses but does not
  // define throws here. The error says which name, which is the whole
  // point of the test.
  assert.doesNotThrow(() => loadMain());
});

test("the main process defines the helpers it uses", () => {
  const sandbox = loadMain();

  // These are the ones the rename took once. They are named here so
  // that the next rename takes them again and this test says so.
  for (const name of ["runtimeDirectory", "runtimeBase", "isMemory"]) {
    assert.equal(typeof sandbox[name], "function", `${name} is not defined`);
  }
});

test("the main process reaches no undefined name", () => {
  const source = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

  // The names the module declares.
  const declared = new Set();
  for (const match of source.matchAll(/^(?:async )?function ([A-Za-z0-9_$]+)/gm)) declared.add(match[1]);
  for (const match of source.matchAll(/^(?:const|let|var) ([A-Za-z0-9_$]+)\s*=/gm)) declared.add(match[1]);
  // An import that is renamed on the way in is still a declaration.
  for (const match of source.matchAll(/^const \{([^}]+)\} = require/gm)) {
    for (const name of match[1].split(",")) {
      const parts = name.trim().split(":");
      declared.add((parts[1] || parts[0]).trim());
    }
  }

  // The names it uses. Only the ones that look like a call of a bare
  // name are checked: a call of a property is somebody else's problem.
  //
  // The words that are not names are left out: `if`, `catch`, `for` and
  // the rest are the language, not the module.
  const words = new Set([
    "if", "else", "for", "while", "do", "switch", "case", "catch", "try",
    "return", "typeof", "new", "await", "async", "function", "delete",
    "void", "in", "of", "instanceof", "class", "super", "this", "throw",
    "yield", "default", "finally", "break", "continue",
  ]);
  // The source with the strings and the comments taken out, so a word
  // in a message is not taken for a name.
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

  // The names that are declared inside a function are not visible to the
  // check above, because the declaration is not at the top level. They
  // are found by looking for every name that is assigned anywhere.
  for (const match of code.matchAll(/\b(?:const|let|var)\s+([A-Za-z0-9_$]+)\s*=/g)) declared.add(match[1]);
  for (const match of code.matchAll(/\bfunction\s+([A-Za-z0-9_$]+)\s*\(/g)) declared.add(match[1]);
  for (const match of code.matchAll(/\(([A-Za-z0-9_$,\s]*)\)\s*=>/g)) {
    for (const name of match[1].split(",")) {
      const clean = name.trim().split(/[=\s]/)[0];
      if (clean) declared.add(clean);
    }
  }

  // The names that come from elsewhere: the modules it requires, the
  // globals, and the built-ins.
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
