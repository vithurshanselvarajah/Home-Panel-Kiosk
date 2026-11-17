

const crypto = require("node:crypto");

const KEY_BYTES = 32;

function generateEncryptionKey() {
  return crypto.randomBytes(KEY_BYTES).toString("base64");
}

function isValidEncryptionKey(key) {
  if (typeof key !== "string" || !key.trim()) return false;
  try {
    const raw = Buffer.from(key.trim(), "base64");

    return raw.length === KEY_BYTES && raw.toString("base64").replace(/=+$/, "") === key.trim().replace(/=+$/, "");
  } catch {
    return false;
  }
}

module.exports = { generateEncryptionKey, isValidEncryptionKey, KEY_BYTES };
