

const { spawnSync } = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const root = path.join(__dirname, "..");
const deviceDir = path.join(root, "device");
const vendorDir = path.join(root, "vendor");

const ORT_VERSION = "1.23.2";

const cacheDir = path.join(os.homedir(), ".cache", "home-panel", "onnxruntime");

const ARCHITECTURES = [
  {
    package: "x64",
    goarch: "amd64",

    release: "onnxruntime-linux-x64",

    cc: "gcc",
  },
  {
    package: "arm64",
    goarch: "arm64",
    release: "onnxruntime-linux-aarch64",
    cc: "aarch64-linux-gnu-gcc",
  },
];

function fail(message) {
  console.error(`build: ${message}`);
  process.exit(1);
}

function fetchRuntime(architecture) {
  const target = path.join(cacheDir, `${architecture.release}-${ORT_VERSION}`);
  const library = path.join(target, "lib", "libonnxruntime.so");
  if (fs.existsSync(library)) {
    return target;
  }

  const archive = path.join(cacheDir, `${architecture.release}-${ORT_VERSION}.tgz`);
  fs.mkdirSync(cacheDir, { recursive: true });

  if (!fs.existsSync(archive)) {
    const url = `https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/${architecture.release}-${ORT_VERSION}.tgz`;
    console.log(`build: fetching the onnx runtime for ${architecture.goarch}`);
    const fetch = spawnSync("curl", ["-sSL", "--fail", "-o", archive, url], { stdio: "inherit" });
    if (fetch.status !== 0) fail(`the onnx runtime for ${architecture.goarch} could not be fetched`);
  }

  console.log(`build: unpacking the onnx runtime for ${architecture.goarch}`);
  const unpack = spawnSync("tar", ["xzf", archive, "-C", cacheDir], { stdio: "inherit" });
  if (unpack.status !== 0) fail(`the onnx runtime for ${architecture.goarch} could not be unpacked`);

  if (!fs.existsSync(library)) {
    fail(`the onnx runtime for ${architecture.goarch} is not where it was expected: ${library}`);
  }
  return target;
}

function findRuntimeLibrary(libraryDirectory) {
  if (!fs.existsSync(libraryDirectory)) return null;
  const candidates = fs
    .readdirSync(libraryDirectory)
    .filter((name) => /^libonnxruntime\.so\.1/.test(name))
    .sort((a, b) => b.length - a.length);
  if (candidates.length === 0) return null;
  return path.join(libraryDirectory, candidates[0]);
}

fs.mkdirSync(vendorDir, { recursive: true });

for (const architecture of ARCHITECTURES) {
  const runtime = fetchRuntime(architecture);
  const libraryDirectory = path.join(runtime, "lib");
  const includeDirectory = path.join(runtime, "include");
  const target = path.join(vendorDir, `home-panel-device-linux-${architecture.goarch}`);
  console.log(`build: the device for ${architecture.goarch}`);

  const runtimeLibrary = findRuntimeLibrary(libraryDirectory);

  const runtimeDirectory = path.join(vendorDir, architecture.goarch);
  fs.mkdirSync(runtimeDirectory, { recursive: true });
  const runtimeTarget = path.join(runtimeDirectory, "libonnxruntime.so.1");
  if (!runtimeLibrary) {
    fail(`the onnx runtime for ${architecture.goarch} was not found in ${libraryDirectory}`);
  }
  fs.copyFileSync(runtimeLibrary, runtimeTarget);
  fs.chmodSync(runtimeTarget, 0o755);
  console.log(`build: the onnx runtime for ${architecture.goarch} is at ${runtimeTarget}`);

  const build = spawnSync("go", [
    "build", "-trimpath", "-ldflags=-s -w",
    "-o", target, ".",
  ], {
    cwd: deviceDir,
    stdio: "inherit",
    env: {
      ...process.env,
      CGO_ENABLED: "1",
      GOOS: "linux",
      GOARCH: architecture.goarch,
      CC: architecture.cc,

      CGO_CFLAGS: `-I${path.join(deviceDir, "wakeword", "ort", "include")} -I${includeDirectory} -O2`,

      CGO_LDFLAGS: `-L${libraryDirectory} -lonnxruntime -Wl,-rpath,$ORIGIN`,
    },
  });
  if (build.status !== 0) {
    console.error(`build: the device for ${architecture.goarch} did not build`);
    process.exit(1);
  }
  console.log(`build: ${target}`);
}
