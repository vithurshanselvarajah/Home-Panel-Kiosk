

const fs = require("node:fs");
const path = require("node:path");

const CACHE_DIRECTORIES = [
  "Cache",
  "Code Cache",
  "GPUCache",
  "GPUPersistentCache",
  "DawnGraphiteCache",
  "DawnWebGPUCache",
  "GrShaderCache",
  "ShaderCache",

  "Dictionaries",

  "Shared Dictionary",
];

function moveCachesToMemory(profile, memory) {
  let moved = 0;
  try {

    fs.mkdirSync(profile, { recursive: true, mode: 0o700 });
  } catch (error) {
    console.warn("the Chromium profile could not be prepared:", error.message);
    return 0;
  }
  for (const name of CACHE_DIRECTORIES) {
    const source = path.join(profile, name);
    const target = path.join(memory, name);

    try {
      if (fs.lstatSync(source).isSymbolicLink() && fs.existsSync(target)) {
        moved += 1;
        continue;
      }
    } catch {  }
    try {
      fs.mkdirSync(target, { recursive: true, mode: 0o700 });

      fs.rmSync(source, { recursive: true, force: true });
      fs.symlinkSync(target, source, "dir");
      moved += 1;
    } catch (error) {

      console.warn("the cache could not be moved into memory:", error.message);
    }
  }
  return moved;
}

module.exports = { moveCachesToMemory, CACHE_DIRECTORIES };
