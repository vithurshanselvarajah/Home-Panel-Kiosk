const test = require("node:test");
const assert = require("node:assert/strict");
const { generateEncryptionKey, isValidEncryptionKey, KEY_BYTES } = require("../src/encryption");

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

  function saveKey(enabled, key) {
    const wanted = Boolean(enabled);
    let value = String(key || "").trim();
    if (!wanted) value = "";
    else if (!value) value = generateEncryptionKey();
    return { enabled: wanted, key: value };
  }

  const off = saveKey(false, "anoldkey");
  assert.equal(off.enabled, false);
  assert.equal(off.key, "");

  const made = saveKey(true, "");
  assert.equal(made.enabled, true);
  assert.equal(isValidEncryptionKey(made.key), true);

  const kept = saveKey(true, "thekey");
  assert.equal(kept.key, "thekey");
});
