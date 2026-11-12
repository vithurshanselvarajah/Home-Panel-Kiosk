// Chromium's caches, moved into memory.
//
// The caches are the parts of Chromium's profile that are caches: they
// are worth nothing between one run and the next, and writing them to
// the disk wears the flash out for nothing.
//
// The move is a bind, not a copy: the directory is a link to a directory
// in memory, so Chromium writes through it and the memory is given back
// when the link is removed.
//
// The link has to be made before Chromium starts. Chromium makes the
// cache directories itself when it runs and keeps them open, so a link
// made afterwards is a link to a directory Chromium is already writing
// to: the name changes but the writes still land on the disk.

const fs = require("node:fs");
const path = require("node:path");

// cacheDirectories are the parts of Chromium's profile that are caches.
//
// They are moved into memory. The names are the ones Chromium uses for
// them under the profile directory.
const CACHE_DIRECTORIES = [
  "Cache",
  "Code Cache",
  "GPUCache",
  "GPUPersistentCache",
  "DawnGraphiteCache",
  "DawnWebGPUCache",
  "GrShaderCache",
  "ShaderCache",
  // The spellchecker writes its dictionary here, whether or not the
  // spellchecker is turned off: the dictionary is downloaded by the
  // browser process before the session is asked to do anything.
  "Dictionaries",
  // The shared dictionary is downloaded the same way.
  "Shared Dictionary",
];

/**
 * Moves Chromium's caches into memory.
 *
 * The link has to be made before Chromium starts, so this is called
 * while the module is loading and not after the app is ready.
 *
 * @param {string} profile the Chromium profile directory
 * @param {string} memory the directory in memory to link them to
 * @returns {number} how many were linked
 */
function moveCachesToMemory(profile, memory) {
  let moved = 0;
  try {
    // Chromium makes the profile when it starts. It has to be there first,
    // or the link has nothing to hang from.
    fs.mkdirSync(profile, { recursive: true, mode: 0o700 });
  } catch (error) {
    console.warn("the Chromium profile could not be prepared:", error.message);
    return 0;
  }
  for (const name of CACHE_DIRECTORIES) {
    const source = path.join(profile, name);
    const target = path.join(memory, name);
    // A link that is already there is done — but only if what it points
    // at is still there.
    //
    // The memory is in /run, which is a fresh filesystem on every boot,
    // so a link made by an earlier run points at a directory that is
    // gone. Chromium then cannot make its cache directory and says so
    // on every start: "Failed to create cache directory ... File exists".
    try {
      if (fs.lstatSync(source).isSymbolicLink() && fs.existsSync(target)) {
        moved += 1;
        continue;
      }
    } catch { /* it is not there yet, which is the usual case */ }
    try {
      fs.mkdirSync(target, { recursive: true, mode: 0o700 });
      // What is in the directory is a cache from a previous run. It is
      // worth nothing, so it goes to make room for the link.
      fs.rmSync(source, { recursive: true, force: true });
      fs.symlinkSync(target, source, "dir");
      moved += 1;
    } catch (error) {
      // A cache that stays on the disk is worse than one in memory, but it
      // is not a failure.
      console.warn("the cache could not be moved into memory:", error.message);
    }
  }
  return moved;
}

module.exports = { moveCachesToMemory, CACHE_DIRECTORIES };
