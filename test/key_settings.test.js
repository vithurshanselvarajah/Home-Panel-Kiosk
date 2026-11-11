// Checks that the encryption key survives a save and reload, and that a
// cleared key stays cleared.
const test = require("node:test");
const assert = require("node:assert/strict");

// The settings logic, taken out of main.js so it can be tested without
// Electron.
function cleanSettings(settings, next, safeStorageAvailable = true) {
  const clean = { ...settings, ...next };
  clean.esphomeEncryptionKey = String(clean.esphomeEncryptionKey || "").trim();
  clean.esphomeDeviceName = String(clean.esphomeDeviceName || "").trim();
  delete clean.esphomeKeepEncryptionKey;
  return clean;
}

function persist(clean, encrypted) {
  const persisted = { ...clean };
  delete persisted.esphomeEncryptionKeyEncrypted;
  delete persisted.esphomeEncryptionKey;
  if (clean.esphomeEncryptionKey && encrypted) {
    persisted.esphomeEncryptionKeyEncrypted = "ENCRYPTED:" + clean.esphomeEncryptionKey;
  }
  return persisted;
}

test("a key is kept when the toggle is on", () => {
  const settings = { esphomeEncryptionKey: "thekey" };
  // The renderer deletes the key from the data when the toggle is on.
  const clean = cleanSettings(settings, {});
  assert.equal(clean.esphomeEncryptionKey, "thekey");
  const persisted = persist(clean, true);
  assert.equal(persisted.esphomeEncryptionKeyEncrypted, "ENCRYPTED:thekey");
});

test("a key is set when one is given", () => {
  const settings = {};
  const clean = cleanSettings(settings, { esphomeEncryptionKey: "newkey" });
  const persisted = persist(clean, true);
  assert.equal(persisted.esphomeEncryptionKeyEncrypted, "ENCRYPTED:newkey");
});

test("a key is cleared when the toggle is off and the field is empty", () => {
  const settings = { esphomeEncryptionKey: "thekey" };
  const clean = cleanSettings(settings, { esphomeEncryptionKey: "" });
  const persisted = persist(clean, true);
  // The old copy is gone, so the key does not come back.
  assert.equal(persisted.esphomeEncryptionKeyEncrypted, undefined);
  assert.equal(persisted.esphomeEncryptionKey, undefined);
});

test("the toggle is not carried about", () => {
  const clean = cleanSettings({}, { esphomeKeepEncryptionKey: true });
  assert.equal("esphomeKeepEncryptionKey" in clean, false);
});
