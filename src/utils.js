function safeUrl(raw) {
  try {
    const url = new URL(String(raw));
    return ["http:", "https:"].includes(url.protocol) ? url.toString() : null;
  } catch {
    return null;
  }
}

function cameraObjectId(name) {
  return `camera_${String(name).toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_|_$/g, "").slice(0, 48)}`;
}

module.exports = { safeUrl, cameraObjectId };
