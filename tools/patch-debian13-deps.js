// Makes the Debian package declare the Debian 13 (trixie) library names.
//
// Electron's packaging helper names the libraries the way Debian 11 named
// them: libgtk-3-0, libatspi2.0-0, libasound2. Debian 13 renamed the
// time_t transition packages, so those names are gone, and the packages the
// panel needs are now libgtk-3-0t64, libatspi2.0-0t64 and libasound2t64.
//
// The names come from the helper's own dependency map, and it merges the
// caller's list with that map rather than replacing it, so a list in the
// forge configuration cannot correct it. The map is patched here instead.
//
// The patch is applied after every install, because the helper is a
// dependency of the forge maker and is not kept in this repository.

const fs = require("node:fs");
const path = require("node:path");

// The package that carries the dependency map, and the file within it.
const PACKAGE = "electron-installer-debian";
const FILE = path.join("src", "dependencies.js");

// The Debian 13 names, by the key the map uses.
const REPLACEMENTS = {
  atspi: "libatspi2.0-0t64",
  gtk3: "libgtk-3-0t64",
};

function patchMap(source) {
  let patched = source;

  for (const [key, value] of Object.entries(REPLACEMENTS)) {
    const pattern = new RegExp(`(\\b${key}:\\s*)'[^']*'`);
    if (!pattern.test(patched)) {
      throw new Error(`the dependency map has no ${key} entry to patch`);
    }
    patched = patched.replace(pattern, `$1'${value}'`);
  }

  return patched;
}

function main() {
  const root = path.join(__dirname, "..");
  const target = path.join(root, "node_modules", PACKAGE, FILE);

  if (!fs.existsSync(target)) {
    console.log(`${PACKAGE} is not installed; nothing to patch`);
    return;
  }

  const source = fs.readFileSync(target, "utf8");

  if (source.includes(REPLACEMENTS.atspi)) {
    console.log(`${PACKAGE} already declares the Debian 13 names`);
    return;
  }

  fs.writeFileSync(target, patchMap(source));
  console.log(`patched ${PACKAGE} to declare the Debian 13 library names`);
}

main();
