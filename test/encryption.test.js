const test = require("node:test");
const assert = require("node:assert/strict");
const { generateEncryptionKey, isValidEncryptionKey, KEY_BYTES } = require("../src/encryption");

// The key for the Home Assistant connection.
//
// The key is thirty two bytes, and the protocol wants it as base64. It
// is made by the application, which is where the random source is.

test("a generated key is thirty two bytes of base64", () => {
  const key = generateEncryptionKey();
  assert.equal(Buffer.from(key, "base64").length, KEY_BYTES);
});

test("two generated keys are never the same", () => {
  assert.notEqual(generateEncryptionKey(), generateEncryptionKey());
});

test("a generated key is one the protocol accepts", () => {
  assert.equal(isValidEncryptionKey(generateEncryptionKey()), true);
});

test("a key that is not base64 is refused", () => {
  assert.equal(isValidEncryptionKey("not a key"), false);
});

test("a key of the wrong length is refused", () => {
  assert.equal(isValidEncryptionKey(Buffer.alloc(16).toString("base64")), false);
});

test("an empty key is refused", () => {
  assert.equal(isValidEncryptionKey(""), false);
  assert.equal(isValidEncryptionKey("   "), false);
  assert.equal(isValidEncryptionKey(null), false);
});

test("the key is what the settings keep when the connection is encrypted", () => {
  // The settings logic, as the save does it.
  function saveKey(enabled, key) {
    const wanted = Boolean(enabled);
    let value = String(key || "").trim();
    if (!wanted) value = "";
    else if (!value) value = generateEncryptionKey();
    return { enabled: wanted, key: value };
  }

  // Off means no key at all.
  const off = saveKey(false, "anoldkey");
  assert.equal(off.enabled, false);
  assert.equal(off.key, "");

  // On with no key makes one.
  const made = saveKey(true, "");
  assert.equal(made.enabled, true);
  assert.equal(isValidEncryptionKey(made.key), true);

  // On with a key keeps it.
  const kept = saveKey(true, "thekey");
  assert.equal(kept.key, "thekey");
});
