const test = require("node:test");
const assert = require("node:assert/strict");
const { safeUrl, editFieldValue, settingNumber, settingBoolean } = require("../src/utils");

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
