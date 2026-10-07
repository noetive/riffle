"use strict";

const { existsSync } = require("node:fs");
const { join } = require("node:path");

// Host to the package carrying its binary. Keep in step with TARGETS in
// scripts/build-platform-packages.js.
const PLATFORM_PACKAGES = {
  "darwin-arm64": "@noetive/riffle-darwin-arm64",
  "darwin-x64": "@noetive/riffle-darwin-x64",
  "linux-arm64": "@noetive/riffle-linux-arm64",
  "linux-x64": "@noetive/riffle-linux-x64",
  "win32-x64": "@noetive/riffle-win32-x64",
};

const RELEASES = "https://github.com/noetive/riffle/releases/download";

const hostKey = (platform = process.platform, arch = process.arch) => `${platform}-${arch}`;
const binaryName = (platform = process.platform) => (platform === "win32" ? "riffle.exe" : "riffle");

// The bare release asset postinstall falls back to, as named by the
// extra_files in .goreleaser.yaml: riffle-darwin-arm64, riffle-win32-x64.exe.
const assetName = (key = hostKey()) => `riffle-${key}${key.startsWith("win32") ? ".exe" : ""}`;
const assetUrl = (version, key = hostKey()) => `${RELEASES}/v${version}/${assetName(key)}`;
const checksumsUrl = (version) => `${RELEASES}/v${version}/checksums.txt`;

function platformPackage(key = hostKey()) {
  const name = PLATFORM_PACKAGES[key];
  if (!name) {
    throw new Error(
      `${key} is not a supported platform. Supported: ${Object.keys(PLATFORM_PACKAGES).join(", ")}. ` +
        `You can still build from source: go install github.com/noetive/riffle/cmd/riffle@latest`,
    );
  }
  return name;
}

// The package's own bin directory, where postinstall writes a downloaded binary.
const defaultFallbackDir = () => join(__dirname, "..", "bin");

function defaultResolvePackage(name) {
  try {
    // package.json resolves even though a binary-only package has no main.
    return join(require.resolve(`${name}/package.json`), "..");
  } catch {
    return undefined;
  }
}

// Finds the native binary. The optional dependency comes first because it works
// even when install scripts are blocked; the postinstall download is the
// fallback. Throws with the remediation when neither is present.
function resolveBinary(options = {}) {
  const platform = options.platform ?? process.platform;
  const arch = options.arch ?? process.arch;
  const exists = options.fileExists ?? existsSync;
  const resolvePackage = options.resolvePackage ?? defaultResolvePackage;

  const packageName = platformPackage(hostKey(platform, arch));
  const executable = binaryName(platform);

  const packageDir = resolvePackage(packageName);
  if (packageDir) {
    const candidate = join(packageDir, "bin", executable);
    if (exists(candidate)) return candidate;
  }

  const fallback = join(options.fallbackDir ?? defaultFallbackDir(), executable);
  if (exists(fallback)) return fallback;

  throw new Error(
    `could not find the riffle binary for ${hostKey(platform, arch)}.\n` +
      `Expected it in the ${packageName} package or at ${fallback}.\n` +
      `Reinstall with \`npm install -g @noetive/riffle\`, or if your package manager blocks install scripts, ` +
      `run \`npm rebuild @noetive/riffle\`.`,
  );
}

module.exports = {
  PLATFORM_PACKAGES,
  assetName,
  assetUrl,
  binaryName,
  checksumsUrl,
  hostKey,
  platformPackage,
  resolveBinary,
};
