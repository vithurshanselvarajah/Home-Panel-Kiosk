const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const root = path.join(__dirname, "..");
const html = fs.readFileSync(path.join(root, "src", "ui", "index.html"), "utf8");
const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");

// The interface is a page with a drawer, a settings panel and a touch
// keyboard. It is one file of markup and one of script, and nothing
// checks that the two agree with each other.
//
// An edit that replaces a file with a piece of it looks fine in a diff
// until the thing is opened: the drawer goes, and the only sign is that
// the page is blank. So the page is checked for the pieces it must have,
// and the two files are checked to name the same things.

test("the interface page is a whole page", () => {
  assert.ok(html.length < 100000, "the page is implausibly large, so it has probably been duplicated");
  assert.ok(html.startsWith("<!doctype html>"), "the page does not start with a doctype");
  assert.ok(html.includes("</html>"), "the page is not closed");
});

test("the drawer has its parts", () => {
  for (const id of [
    "side-panel",
    "side-panel-backdrop",
    "brightness-control",
    "volume-control",
    "control-state",
    "immich-state",
    "dashboard-state",
    "voice-row",
    "settings-button",
    "display-mode-button",
    "exit-button",
    "mode-label",
    "version-label",
  ]) {
    assert.ok(html.includes(`id="${id}"`), `the drawer has no ${id}`);
  }
});

test("the settings panel has its parts", () => {
  for (const id of [
    "settings-panel",
    "settings-form",
    "tab-device",
    "tab-voice",
    "settings-view-device",
    "settings-view-voice",
    "voice-form",
    "form-error",
  ]) {
    assert.ok(html.includes(`id="${id}"`), `the settings panel has no ${id}`);
  }
});

test("the page loads the scripts it needs", () => {
  for (const source of ["./dev-bridge.js", "./device-ui.js", "./renderer.js", "./style.css"]) {
    assert.ok(html.includes(source), `the page does not load ${source}`);
  }
});

test("the drawer and the script name the same things", () => {
  // The script looks the drawer up by id, so an id in one and not the
  // other is a part of the drawer that will never be filled in.
  const ids = [...renderer.matchAll(/\$\("#([A-Za-z0-9_-]+)"\)/g)].map((match) => match[1]);
  assert.ok(ids.length > 10, "the script does not look up enough of the page");
  for (const id of ids) {
    assert.ok(html.includes(`id="${id}"`), `the script looks up ${id}, which the page does not have`);
  }
});

test("the voice row is in the drawer and not the settings", () => {
  // The row belongs to the drawer: it is one of the services a person
  // sees without opening the settings.
  const drawer = html.slice(html.indexOf('id="side-panel"'), html.indexOf('id="settings-panel"'));
  assert.ok(drawer.includes('id="voice-row"'), "the voice row is not in the drawer");
  assert.ok(!drawer.includes('id="voice-form"'), "the voice form is in the drawer");
});

test("the music panel has its parts", () => {
  for (const id of [
    "media-panel",
    "media-artwork",
    "media-title",
    "media-artist",
    "media-position",
    "media-elapsed",
    "media-duration",
    "media-play",
    "media-previous",
    "media-next",
  ]) {
    assert.ok(html.includes(`id="${id}"`), `the drawer has no ${id}`);
  }
});

test("the music panel is in the drawer", () => {
  // The music is what the drawer is for, so it belongs in the drawer and
  // not on its own page.
  const drawer = html.slice(html.indexOf('id="side-panel"'), html.indexOf('id="settings-panel"'));
  assert.ok(drawer.includes('id="media-panel"'), "the music panel is not in the drawer");
});

test("the status is below the actions in the drawer", () => {
  // The status says whether the panel is working, and it is looked at
  // rather than pressed, so it is at the bottom where it is out of the
  // way.
  const drawer = html.slice(html.indexOf('id="side-panel"'), html.indexOf('id="settings-panel"'));
  const status = drawer.indexOf('class="service-status"');
  const actions = drawer.indexOf('class="drawer-actions"');
  assert.ok(status > 0, "the drawer has no status block");
  assert.ok(actions > 0, "the drawer has no actions");
  assert.ok(status > actions, "the status is above the actions in the drawer");
});

test("the transport buttons say what they are for", () => {
  // Each button names its command, and the script sends what the button
  // says. A button without a name would send nothing.
  for (const action of ["previous", "play", "next"]) {
    assert.ok(html.includes(`data-transport="${action}"`), `there is no ${action} button`);
  }
  assert.ok(renderer.includes('data-transport'), "the script does not read the transport buttons");
});

test("a press in the drawer is not a drag", () => {
  // The drawer follows the finger when it is dragged. A press on a
  // button in the open drawer is not a drag, and treating it as one
  // moved the drawer and then put it back, which is the bouncing.
  //
  // The drag only begins once the pointer has moved far enough to be a
  // drag and not a press.
  assert.ok(
    renderer.includes("dragState.moved"),
    "the drawer does not tell a press from a drag",
  );
  assert.ok(
    renderer.includes("if (!dragState.moved)"),
    "the drawer starts dragging on a press",
  );
});

test("the drawer is only told about a change of state", () => {
  // The main process resizes the overlay view when the drawer is told
  // it is open, and the drawer jumps when the view is resized under it.
  // A press in the open drawer must not tell it anything.
  assert.ok(
    renderer.includes("if (wasOpen !== Boolean(open))"),
    "the drawer tells the main process about a press",
  );
});

test("the position is carried on between the server's updates", () => {
  // The server says where the track has got to when it says so, and a
  // scrubber that jumps from one update to the next does not look like
  // music. The panel carries the position on from the last update.
  assert.ok(
    renderer.includes("function currentPosition()"),
    "the position is not carried on",
  );
  assert.ok(
    renderer.includes("setInterval"),
    "the position is not redrawn as the music plays",
  );
});

test("the drawer shows the music's transport without closing", () => {
  // The music's buttons are inside the drawer, and the drawer used to
  // close on every button: the music was impossible to control.
  //
  // The behaviour is checked in drawer.test.js, by pressing the real
  // buttons. This is here so that the check is not lost.
  assert.ok(
    renderer.includes("if (button.dataset.transport"),
    "the drawer closes when the music's buttons are pressed",
  );
});

test("the drawer can show either music", () => {
  // The music server's music and Home Assistant's own music are two
  // different things, and the panel shows one or the other.
  assert.ok(html.includes('id="media-source-server"'), "there is no music server source");
  assert.ok(html.includes('id="media-source-home"'), "there is no Home Assistant source");
  assert.ok(renderer.includes("mediaSources"), "the two musics are not kept apart");
  assert.ok(
    renderer.includes("source: mediaSource"),
    "the transport command does not say which music it is for",
  );
});

test("there is a wake word overlay", () => {
  // The overlay covers the screen while the assistant is working and it
  // is where the words are shown.
  assert.ok(html.includes('id="voice-overlay"'), "there is no wake word overlay");
  assert.ok(html.includes('id="voice-overlay-heard"'), "the overlay has nowhere to show what was heard");
  assert.ok(html.includes('id="voice-overlay-said"'), "the overlay has nowhere to show what is being said");
  assert.ok(renderer.includes("overlay:"), "the overlay is not wired up");
});

test("the wake word settings are Home Assistant's to set", () => {
  // They are entities on the panel, so Home Assistant reads and changes
  // them. The panel's own form must not write them, or it would undo
  // whatever Home Assistant had set.
  assert.ok(
    !renderer.includes("voiceWakeThreshold"),
    "the panel still writes the wake word threshold",
  );
  assert.ok(
    !renderer.includes("voiceWakeWordEnabled"),
    "the panel still writes the wake word switch",
  );
});

test("the wake word overlay covers the whole screen", () => {
  // The overlay is drawn in a view that the main process sizes. The
  // indicator is a strip along the bottom; the overlay covers
  // everything, so the main process has to be told when it is up.
  assert.ok(
    renderer.includes("window.homePanel.voiceOverlay(overlayUp)"),
    "the main process is not told when the overlay comes and goes",
  );
  assert.ok(
    fs.readFileSync(path.join(root, "src", "main.js"), "utf8").includes("voiceOverlayFull"),
    "the main process does not size the view for the overlay",
  );
});

test("the wake word overlay stays up and can be swiped away", () => {
  // The overlay closes the instant the assistant's flags go quiet, which
  // is between the microphone closing and the reply starting. It is held
  // instead, and a person can swipe it away before the hold is over.
  assert.ok(
    renderer.includes("OVERLAY_HOLD_MS") || fs.readFileSync(path.join(root, "src", "ui", "device-ui.js"), "utf8").includes("OVERLAY_HOLD_MS"),
    "the overlay is not held open after the assistant finishes",
  );
  const ui = fs.readFileSync(path.join(root, "src", "ui", "device-ui.js"), "utf8");
  assert.ok(ui.includes("installGesture"), "the overlay cannot be swiped away");
  assert.ok(ui.includes("is-dismissing"), "the overlay does not follow the finger");
});

test("the overlay shows what was heard and what is being said", () => {
  // The question is above the answer, so the screen reads as a question
  // and an answer rather than as one block of text.
  assert.ok(html.includes('id="voice-overlay-heard"'), "there is nowhere for what was heard");
  assert.ok(html.includes('id="voice-overlay-said"'), "there is nowhere for what is being said");
});

test("the overlay says listening while it thinks", () => {
  // Thinking is still listening: the panel is hearing the room and
  // working out what to say. The two look the same.
  const ui = fs.readFileSync(path.join(root, "src", "ui", "device-ui.js"), "utf8");
  assert.ok(
    ui.includes('thinking: "Listening"'),
    "the overlay says thinking",
  );
});

test("the overlay dismisses itself after the hold", () => {
  // The overlay holds for a few seconds after the assistant has
  // finished, and then goes away on its own.
  const ui = fs.readFileSync(path.join(root, "src", "ui", "device-ui.js"), "utf8");
  assert.ok(ui.includes("OVERLAY_HOLD_MS = 5000"), "the hold is not five seconds");
  assert.ok(
    ui.includes("if (this.holding && this.holdUntil"),
    "the overlay is not dismissed when the hold is over",
  );
  // The hold runs only when the work is done, so a long answer is not
  // cut off by it.
  assert.ok(
    ui.includes("this.holding = false;"),
    "the hold runs while there is work",
  );
});

test("the answer's words are revealed with the reply", () => {
  // The words are shown in step with the audio, so the reply is not
  // blank until it is over.
  const ui = fs.readFileSync(path.join(root, "src", "ui", "device-ui.js"), "utf8");
  assert.ok(ui.includes("revealSaid"), "the answer is not revealed");
  assert.ok(ui.includes("setProgress"), "the progress of the reply is not used");
});
