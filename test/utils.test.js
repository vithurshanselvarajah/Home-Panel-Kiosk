const test = require("node:test");
const assert = require("node:assert/strict");
const { safeUrl, cameraObjectId } = require("../src/utils");

test("safeUrl accepts and normalizes HTTP URLs", () => {
  assert.equal(safeUrl("https://example.com/camera"), "https://example.com/camera");
  assert.equal(safeUrl("http://example.com"), "http://example.com/");
});

test("safeUrl rejects unsupported and invalid URLs", () => {
  assert.equal(safeUrl("javascript:alert(1)"), null);
  assert.equal(safeUrl("file:///etc/passwd"), null);
  assert.equal(safeUrl("/relative/path"), null);
});

test("cameraObjectId lowercases and replaces non-alphanumeric runs", () => {
  assert.equal(cameraObjectId("Front Door Camera!"), "camera_front_door_camera");
});

test("cameraObjectId trims separators and limits the entity suffix", () => {
  assert.equal(cameraObjectId("__Patio__"), "camera_patio");
  assert.equal(cameraObjectId("A".repeat(60)), `camera_${"a".repeat(48)}`);
});
