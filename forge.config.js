const { FusesPlugin } = require("@electron-forge/plugin-fuses");
const { FuseV1Options, FuseVersion } = require("@electron/fuses");

module.exports = {
  packagerConfig: {
    asar: true,
    executableName: "homepanel-kiosk",
    appBundleId: "io.homepanel.kiosk",
    extraResource: ["packaging/homepanel-kiosk.service"],
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
      draft: true,
    },
  }] : [],
};
