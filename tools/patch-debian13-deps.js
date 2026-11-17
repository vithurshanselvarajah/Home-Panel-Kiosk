

const fs = require("node:fs");
const path = require("node:path");

const PACKAGE = "electron-installer-debian";
const FILE = path.join("src", "dependencies.js");

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
