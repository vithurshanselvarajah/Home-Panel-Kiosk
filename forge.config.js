const { FusesPlugin } = require("@electron-forge/plugin-fuses");
const { FuseV1Options, FuseVersion } = require("@electron/fuses");
const { copySidecar, clearSandboxSetuid } = require("./tools/copy-sidecar");

module.exports = {
  packagerConfig: {
    asar: true,
    executableName: "homepanel-kiosk",
    appBundleId: "io.homepanel.kiosk",
    extraResource: ["packaging/homepanel-kiosk.service"],
  },
  hooks: {
    // The EchoMuse sidecar is a separate process and has to be executable, so
    // it is placed beside the Electron binary rather than inside the asar.
    async postPackage(forgeConfig, context) {
      for (const outputPath of context.outputPaths) {
        copySidecar(outputPath);
      }
      return [];
    },
    // The Chromium sandbox helper is made not setuid in the packages that
    // were built, so the browser process is not re-executed through it and
    // keeps the environment it needs to find the Wayland socket.
    async postMake(forgeConfig, makeResults) {
      for (const result of makeResults) {
        for (const artifact of result.artifacts || []) {
          clearSandboxSetuid(artifact);
        }
      }
      return [];
    },
  },
  makers: [
    {
      name: "@electron-forge/maker-deb",
      config: {
        options: {
          // Electron's shared libraries are supplied by Debian rather than
          // bundled with the application. Keep the image's runtime contract
          // explicit so a minimal rootfs can install the package reliably.
          //
          // The names are the Debian 13 ones: Debian renamed the packages
          // whose time_t changed, so libgtk-3-0 is now libgtk-3-0t64 and
          // libatspi2.0-0 is now libatspi2.0-0t64. The names must be in
          // `options`; the maker passes the rest of the configuration beside
          // it, and the installer reads the dependencies from there.
          depends: [
            "ffmpeg",
            "libasound2t64",
            "libatk-bridge2.0-0t64",
            "libatk1.0-0t64",
            "libatspi2.0-0t64",
            "libcups2t64",
            "libdrm2",
            "libgbm1",
            "libglib2.0-0t64",
            "libglib2.0-bin",
            "libgtk-3-0t64",
            "libnss3",
            "libnspr4",
            "libx11-6",
            "libx11-xcb1",
            "libxcomposite1",
            "libxdamage1",
            "libxext6",
            "libxfixes3",
            "libxkbcommon0",
            "libxrandr2",
            "libxrender1",
            "libxshmfence1",
            "libxss1",
            "libxtst6",
            "xdg-utils",
          ],
        },
      },
    },
    { name: "@electron-forge/maker-zip", platforms: ["linux"], config: {} },
  ],
  plugins: [
    new FusesPlugin({
      version: FuseVersion.V1,
      [FuseV1Options.RunAsNode]: false,
      [FuseV1Options.EnableNodeOptionsEnvironmentVariable]: false,
      [FuseV1Options.EnableNodeCliInspectArguments]: false,
      [FuseV1Options.OnlyLoadAppFromAsar]: true,
    }),
  ],
  publishers: process.env.GITHUB_REPOSITORY ? [{
    name: "@electron-forge/publisher-github",
    config: {
      repository: Object.fromEntries(["owner", "name"].map((key, index) => [key, process.env.GITHUB_REPOSITORY.split("/")[index]])),
      draft: false,
    },
  }] : [],
};
