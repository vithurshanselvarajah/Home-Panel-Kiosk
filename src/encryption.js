// The encryption key for the Home Assistant connection.
//
// The key is thirty two bytes, and the protocol wants it as base64. It
// is made from the system's own random source, so two panels never get
// the same one.
//
// The key is a setting that is read and copied into Home Assistant, not
// a password that is typed once and forgotten, so it is shown rather
// than hidden.

const crypto = require("node:crypto");

// The length of the key, in bytes.
//
// The protocol says thirty two. Anything else is refused.
const KEY_BYTES = 32;

/**
 * Makes a key for the Home Assistant connection.
 *
 * @returns {string} the key, as base64
 */
function generateEncryptionKey() {
  return crypto.randomBytes(KEY_BYTES).toString("base64");
}

/**
 * Says whether a key is one the protocol will accept.
 *
 * A key that is not thirty two bytes is refused by the device when it
 * is read, so it is worth saying here rather than at the point where
 * the connection fails.
 *
 * @param {string} key the key
 * @returns {boolean} whether the key is usable
 */
function isValidEncryptionKey(key) {
  if (typeof key !== "string" || !key.trim()) return false;
  try {
    const raw = Buffer.from(key.trim(), "base64");
    // The base64 of thirty two bytes is forty four characters, and the
    // last one has padding. A key of the right length that is not the
    // right base64 is not a key.
    return raw.length === KEY_BYTES && raw.toString("base64").replace(/=+$/, "") === key.trim().replace(/=+$/, "");
  } catch {
    return false;
  }
}

module.exports = { generateEncryptionKey, isValidEncryptionKey, KEY_BYTES };
