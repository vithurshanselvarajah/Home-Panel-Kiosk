const test = require("node:test");
const assert = require("node:assert/strict");
const { safeUrl, editFieldValue, settingNumber, settingBoolean, displayMode, windowBounds } = require("../src/utils");

test("safeUrl accepts and normalizes HTTP URLs", () => {
  assert.equal(safeUrl("https://example.com/camera"), "https://example.com/camera");
  assert.equal(safeUrl("http://example.com"), "http://example.com/");
});

test("safeUrl rejects unsupported and invalid URLs", () => {
  assert.equal(safeUrl("javascript:alert(1)"), null);
  assert.equal(safeUrl("file:///etc/passwd"), null);
  assert.equal(safeUrl("/relative/path"), null);
});

test("editFieldValue inserts characters at the caret", () => {
  const field = { value: "http://ha", type: "url", selectionStart: 9, selectionEnd: 9 };
  assert.deepEqual(editFieldValue(field, "."), { value: "http://ha.", caret: 10 });
});

test("editFieldValue replaces the selected range", () => {
  const field = { value: "mqtt://old", type: "url", selectionStart: 7, selectionEnd: 10 };
  assert.deepEqual(editFieldValue(field, "new"), { value: "mqtt://new", caret: 10 });
});

test("editFieldValue backspace removes the preceding character", () => {
  const field = { value: "http://ha", type: "url", selectionStart: 9, selectionEnd: 9 };
  assert.deepEqual(editFieldValue(field, "BACKSPACE"), { value: "http://h", caret: 8 });
});

test("editFieldValue appends when no caret is reported", () => {
  const field = { value: "abc", type: "text" };
  assert.deepEqual(editFieldValue(field, "d"), { value: "abcd", caret: 4 });
});

test("editFieldValue inserts a space for the space key", () => {
  const field = { value: "a b", type: "text", selectionStart: 3, selectionEnd: 3 };
  assert.deepEqual(editFieldValue(field, "SPACE"), { value: "a b ", caret: 4 });
});

test("editFieldValue rejects non-numeric keys for number fields", () => {
  const field = { value: "12", type: "number", selectionStart: 2, selectionEnd: 2 };
  assert.deepEqual(editFieldValue(field, "a"), { value: "12", caret: 2, rejected: true });
  assert.deepEqual(editFieldValue(field, "3"), { value: "123", caret: 3 });
});

test("editFieldValue ignores toggles and non-editing keys", () => {
  assert.equal(editFieldValue({ value: "on", type: "checkbox" }, "x"), null);
  assert.equal(editFieldValue({ value: "", type: "text" }, "ENTER"), null);
  assert.equal(editFieldValue({ value: "", type: "text" }, "SHIFT"), null);
});

test("settingNumber leaves an unset setting unset", () => {
  assert.equal(settingNumber(undefined), undefined);
  assert.equal(settingNumber(null), undefined);
  assert.equal(settingNumber(""), undefined);
});

test("settingNumber keeps a zero", () => {
  assert.equal(settingNumber(0), 0);
  assert.equal(settingNumber("0"), 0);
});

test("settingNumber reads a number from a string", () => {
  assert.equal(settingNumber("4.5"), 4.5);
  assert.equal(settingNumber(300), 300);
});

test("settingNumber rejects a value that is not a number", () => {
  assert.equal(settingNumber("loud"), undefined);
  assert.equal(settingNumber(Number.NaN), undefined);
});

test("settingBoolean distinguishes an unticked box from an untouched one", () => {
  assert.equal(settingBoolean(false), false);
  assert.equal(settingBoolean(true), true);
  assert.equal(settingBoolean(undefined), undefined);
});

test("displayMode falls back to fullscreen", () => {
  assert.equal(displayMode("fullscreen"), "fullscreen");
  assert.equal(displayMode("window"), "window");
  // A device that was set up before the setting existed has none.
  assert.equal(displayMode(undefined), "fullscreen");
  assert.equal(displayMode(null), "fullscreen");
  assert.equal(displayMode(""), "fullscreen");
});

test("displayMode rejects a mode nobody knows", () => {
  assert.equal(displayMode("kiosk"), "fullscreen");
  assert.equal(displayMode("minimised"), "fullscreen");
  assert.equal(displayMode(7), "fullscreen");
});

test("windowBounds leaves room around the window", () => {
  const bounds = windowBounds({ x: 0, y: 0, width: 1920, height: 1080 });
  assert.ok(bounds.width < 1920, "the window fills the screen");
  assert.ok(bounds.height < 1080, "the window fills the screen");
});

test("windowBounds centres the window", () => {
  const bounds = windowBounds({ x: 0, y: 0, width: 1920, height: 1080 });
  const left = bounds.x;
  const right = 1920 - (bounds.x + bounds.width);
  assert.ok(Math.abs(left - right) <= 1, `the window is off centre by ${Math.abs(left - right)} pixels`);
});

test("windowBounds keeps the window usable on a small screen", () => {
  const bounds = windowBounds({ x: 0, y: 0, width: 800, height: 480 });
  assert.ok(bounds.width >= 900, "the window is too narrow to use");
  assert.ok(bounds.height >= 600, "the window is too short to use");
});

test("windowBounds keeps the window within the screen", () => {
  const bounds = windowBounds({ x: 100, y: 50, width: 3840, height: 2160 });
  assert.ok(bounds.x >= 100, "the window is off the left of the screen");
  assert.ok(bounds.y >= 50, "the window is off the top of the screen");
  assert.ok(bounds.x + bounds.width <= 3940, "the window is off the right of the screen");
  assert.ok(bounds.y + bounds.height <= 2210, "the window is off the bottom of the screen");
});
