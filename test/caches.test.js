const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const { moveCachesToMemory, CACHE_DIRECTORIES } = require("../src/caches");

function temporaryProfile() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "homepanel-caches-"));
  return {
    root,
    profile: path.join(root, "profile"),
    memory: path.join(root, "memory"),
    clean() { fs.rmSync(root, { recursive: true, force: true }); },
  };
}

test("the caches are linked into memory", () => {
  const { profile, memory, clean } = temporaryProfile();
  try {
    const moved = moveCachesToMemory(profile, memory);
    assert.ok(moved > 0, "nothing was moved");
    for (const name of CACHE_DIRECTORIES) {
      const source = path.join(profile, name);
      const target = path.join(memory, name);
      assert.ok(fs.lstatSync(source).isSymbolicLink(), `${name} is not a link`);
      assert.ok(fs.existsSync(target), `${name} points at nothing`);
    }
  } finally {
    clean();
  }
});

test("a link to a directory that is gone is made again", () => {

  const { profile, memory, clean } = temporaryProfile();
  try {
    fs.mkdirSync(profile, { recursive: true });
    fs.mkdirSync(memory, { recursive: true });

    const name = CACHE_DIRECTORIES[0];
    fs.symlinkSync(path.join(memory, name), path.join(profile, name), "dir");
    assert.ok(!fs.existsSync(path.join(memory, name)), "the target should not be there");

    moveCachesToMemory(profile, memory);

    const source = path.join(profile, name);
    const target = path.join(memory, name);
    assert.ok(fs.lstatSync(source).isSymbolicLink(), "the link was not made");
    assert.ok(fs.existsSync(target), "the link still points at nothing");
  } finally {
    clean();
  }
});

test("a link that is already right is left alone", () => {
  const { profile, memory, clean } = temporaryProfile();
  try {
    moveCachesToMemory(profile, memory);
    const name = CACHE_DIRECTORIES[0];
    const source = path.join(profile, name);
    const before = fs.readlinkSync(source);

    moveCachesToMemory(profile, memory);

    assert.equal(fs.readlinkSync(source), before, "the link was changed");
  } finally {
    clean();
  }
});
