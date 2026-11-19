"use strict";

/**
 * Fetches the ONNX Runtime that the device program is linked against.
 *
 * The runtime used to come from the ONNX Runtime project's own GitHub
 * releases. Those releases are gone: microsoft/onnxruntime answers 404, and
 * so does every release asset URL under it, so the tarballs this used to
 * download are no longer there. The project's release build failed on it in
 * CI before this changed anything.
 *
 * The library itself is still published, on PyPI, for both Linux
 * architectures. The wheel carries the same shared object as the release
 * tarball did: the same soname (libonnxruntime.so.1), the same exported
 * entry point (OrtGetApiBase), and the same C API version. The wheel is
 * downloaded, the one shared object is taken out of it, and that is what the
 * device program is linked against.
 *
 * The wheel is a zip file, so it is read here rather than with an external
 * unzip: the build should not need anything that is not already needed to
 * build the panel.
 *
 * The library is pinned by the SHA-256 of the shared object, not of the
 * wheel. The shared object is the thing that is loaded into the device
 * program's process, so it is the thing worth pinning, and the pin survives
 * a wheel being rebuilt for another Python version.
 */

const { createHash } = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const zlib = require("node:zlib");

const ORT_VERSION = "1.23.2";

const CACHE = path.join(os.homedir(), ".cache", "home-panel", "onnxruntime");

const PYPI = "https://pypi.org/pypi/onnxruntime";

/**
 * The architectures the device program is built for.
 *
 * The digest is the SHA-256 of the shared object inside the wheel. Every
 * wheel for a given architecture carries the same one, whatever Python
 * version it was built for.
 */
const ARCHITECTURES = {
  amd64: {
    release: "onnxruntime-linux-x64",
    goarch: "amd64",
    cc: "gcc",
    wheelTag: "x86_64",
    digest: "718c3fb0785d5ea06061b92c32ce4ad35be1bd4c71ae6192efe06f1ab21f0f8a",
  },
  arm64: {
    release: "onnxruntime-linux-aarch64",
    goarch: "arm64",
    cc: "aarch64-linux-gnu-gcc",
    wheelTag: "aarch64",
    digest: "ab8c4363e06ac80b3d1279ea55ebea44e906c5b131ba783ff684a067540c0e94",
  },
};

/** The directory the runtime for an architecture is cached in. */
function runtimeDirectory(architecture) {
  return path.join(CACHE, `${ARCHITECTURES[architecture].release}-${ORT_VERSION}`);
}

/** The directory the library itself is in. */
function libraryDirectory(architecture) {
  return path.join(runtimeDirectory(architecture), "lib");
}

/** The shared object itself, once it has been fetched. */
function libraryPath(architecture) {
  return path.join(libraryDirectory(architecture), `libonnxruntime.so.${ORT_VERSION}`);
}

/**
 * The flags the Go build needs to link against the runtime.
 *
 * The runpath is the library directory so a test binary finds the runtime
 * where it was fetched, rather than wherever the system keeps one.
 */
function ldflags(architecture) {
  const directory = libraryDirectory(architecture);
  return `-L${directory} -lonnxruntime -Wl,-rpath,${directory}`;
}

function digestOf(buffer) {
  return createHash("sha256").update(buffer).digest("hex");
}

async function download(url) {
  const response = await fetch(url);
  if (!response.ok) {
    throw new Error(`${url} answered ${response.status}`);
  }
  return Buffer.from(await response.arrayBuffer());
}

/**
 * Reads one file out of a zip file.
 *
 * A wheel is a zip file with the shared object in it. Only the one file is
 * read, and it is read by name, so the rest of the wheel is left alone.
 */
function readFromZip(archive, pattern) {
  const end = findEndOfCentralDirectory(archive);
  if (end < 0) {
    throw new Error("the download is not a zip file");
  }

  const entries = archive.readUInt16LE(end + 10);
  let offset = archive.readUInt32LE(end + 16);

  for (let i = 0; i < entries; i++) {
    if (archive.readUInt32LE(offset) !== 0x02014b50) {
      throw new Error("the zip file is damaged");
    }
    const method = archive.readUInt16LE(offset + 10);
    const compressed = archive.readUInt32LE(offset + 20);
    const nameLength = archive.readUInt16LE(offset + 28);
    const extraLength = archive.readUInt16LE(offset + 30);
    const commentLength = archive.readUInt16LE(offset + 32);
    const local = archive.readUInt32LE(offset + 42);
    const name = archive.toString("utf8", offset + 46, offset + 46 + nameLength);

    if (pattern.test(name)) {
      if (compressed === 0xffffffff || local === 0xffffffff) {
        throw new Error("the wheel is too large for this reader");
      }
      const localName = archive.readUInt16LE(local + 26);
      const localExtra = archive.readUInt16LE(local + 28);
      const start = local + 30 + localName + localExtra;
      const data = archive.subarray(start, start + compressed);
      return method === 0 ? Buffer.from(data) : zlib.inflateRawSync(data);
    }

    offset += 46 + nameLength + extraLength + commentLength;
  }
  return null;
}

/** The end of the central directory is at the end of the file, after a comment. */
function findEndOfCentralDirectory(archive) {
  const signature = 0x06054b50;
  const earliest = Math.max(0, archive.length - 0xffff - 22);
  for (let i = archive.length - 22; i >= earliest; i--) {
    if (archive.readUInt32LE(i) === signature) return i;
  }
  return -1;
}

/** The wheel that carries the runtime for an architecture. */
async function findWheel(architecture) {
  const wanted = ARCHITECTURES[architecture];
  const response = await fetch(`${PYPI}/${ORT_VERSION}/json`);
  if (!response.ok) {
    throw new Error(`pypi answered ${response.status} for onnxruntime ${ORT_VERSION}`);
  }
  const listing = await response.json();

  const wheels = (listing.urls || []).filter(
    (file) =>
      file.filename.endsWith(".whl") &&
      file.filename.includes("-cp312-cp312-manylinux") &&
      file.filename.endsWith(`_${wanted.wheelTag}.whl`)
  );
  if (wheels.length === 0) {
    throw new Error(`pypi has no wheel for ${wanted.wheelTag} at ${ORT_VERSION}`);
  }
  if (wheels.length > 1) {
    throw new Error(`pypi has ${wheels.length} wheels for ${wanted.wheelTag} at ${ORT_VERSION}`);
  }
  return wheels[0];
}

/**
 * Fetches the runtime for an architecture, unless it is already cached.
 *
 * The cached copy is checked against the pin as well, so a cache that was
 * filled by an older build, or by hand, is not trusted on its name alone.
 */
async function ensureRuntime(architecture) {
  if (!ARCHITECTURES[architecture]) {
    throw new Error(`${architecture} is not an architecture the device program is built for`);
  }

  const library = libraryPath(architecture);
  if (fs.existsSync(library)) {
    const found = digestOf(fs.readFileSync(library));
    if (found === ARCHITECTURES[architecture].digest) {
      return { directory: libraryDirectory(architecture), library };
    }
    fs.rmSync(library);
  }

  console.log(`onnxruntime: fetching the runtime for ${ARCHITECTURES[architecture].goarch}`);
  const wheel = await findWheel(architecture);

  const archive = await download(wheel.url);
  const found = digestOf(archive);
  if (found !== wheel.digests.sha256) {
    throw new Error(
      `the wheel for ${ARCHITECTURES[architecture].goarch} has the digest ${found}, ` +
        `not the ${wheel.digests.sha256} pypi says it has`
    );
  }

  const shared = readFromZip(archive, /^onnxruntime\/capi\/libonnxruntime\.so\./);
  if (!shared) {
    throw new Error(`the wheel for ${ARCHITECTURES[architecture].goarch} has no runtime in it`);
  }
  const sharedDigest = digestOf(shared);
  if (sharedDigest !== ARCHITECTURES[architecture].digest) {
    throw new Error(
      `the runtime in the wheel for ${ARCHITECTURES[architecture].goarch} has the digest ` +
        `${sharedDigest}, not the ${ARCHITECTURES[architecture].digest} it is pinned to`
    );
  }

  const directory = libraryDirectory(architecture);
  fs.mkdirSync(directory, { recursive: true });
  fs.writeFileSync(library, shared);
  fs.chmodSync(library, 0o755);

  // The linker looks for libonnxruntime.so and the loader looks for
  // libonnxruntime.so.1, which is the soname inside the library. Neither
  // name is the file's own name, so both are made here.
  for (const name of ["libonnxruntime.so", "libonnxruntime.so.1"]) {
    const link = path.join(directory, name);
    if (fs.existsSync(link)) fs.rmSync(link);
    fs.symlinkSync(path.basename(library), link);
  }

  console.log(`onnxruntime: the runtime for ${ARCHITECTURES[architecture].goarch} is at ${library}`);
  return { directory, library };
}

/** The architecture the machine this is running on is. */
function hostArchitecture() {
  return os.arch() === "arm64" ? "arm64" : "amd64";
}

async function main(argv) {
  const args = argv.slice(2);

  const flags = args.indexOf("--ldflags");
  if (flags >= 0) {
    const architecture = args[flags + 1] || hostArchitecture();
    // The flags are printed on their own so a workflow can take them as a
    // step output and put them in CGO_LDFLAGS.
    console.log(ldflags(architecture));
    return;
  }

  const wanted = args.filter((argument) => !argument.startsWith("--"));
  const architectures =
    wanted.length === 0
      ? [hostArchitecture()]
      : wanted.includes("all")
        ? Object.keys(ARCHITECTURES)
        : wanted;

  for (const architecture of architectures) {
    await ensureRuntime(architecture);
  }

  // A workflow that runs the tests needs the flags to link against the
  // runtime, so they are written where the workflow can pick them up.
  writeOutput("ldflags", ldflags(hostArchitecture()));
}

/**
 * Writes a value to the workflow's output file.
 *
 * GitHub Actions reads these from the file named by GITHUB_OUTPUT. Outside a
 * workflow there is no such file, so nothing is written.
 */
function writeOutput(name, value) {
  const file = process.env.GITHUB_OUTPUT;
  if (!file) return;
  fs.appendFileSync(file, `${name}=${value}\n`);
}

module.exports = {
  ARCHITECTURES,
  CACHE,
  ORT_VERSION,
  ensureRuntime,
  hostArchitecture,
  ldflags,
  libraryDirectory,
  libraryPath,
  readFromZip,
  runtimeDirectory,
};

if (require.main === module) {
  main(process.argv).catch((error) => {
    console.error(`onnxruntime: ${error.message}`);
    process.exit(1);
  });
}
