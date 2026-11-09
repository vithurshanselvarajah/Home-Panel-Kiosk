const { FusesPlugin } = require("@electron-forge/plugin-fuses");
const { FuseV1Options, FuseVersion } = require("@electron/fuses");
const { copySidecar } = require("./tools/copy-sidecar");

module.exports = {
  packagerConfig: {
    asar: true,
    executableName: "homepanel-kiosk",
    appBundleId: "io.homepanel.kiosk",
    extraResource: ["packaging/homepanel-kiosk.service"],
    // The MQTT client ships a browser bundle and TypeScript build output that
    // the Node build never loads. Leaving them out keeps the package smaller.
    ignore: [
      /^node_modules\/mqtt\/dist\//,
      /^node_modules\/mqtt\/build\/.*\.(map|d\.ts)$/,
      /^node_modules\/mqtt\/build\/.*\.tsbuildinfo$/,
      /^node_modules\/mqtt\/help\//,
      /^node_modules\/mqtt\/CONTRIBUTING\.md$/,
      /^node_modules\/mqtt\/README\.md$/,
    ],
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
  },
  makers: [
    { name: "@electron-forge/maker-deb", config: {} },
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
