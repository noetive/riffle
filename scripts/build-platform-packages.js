#!/usr/bin/env node
"use strict";

// Assembles one npm package per platform, each carrying a single binary, and
// pins installer/package.json's optionalDependencies to them.
//
//     node scripts/stamp-version.js 1.4.0     # first: the versions must match
//     node scripts/build-platform-packages.js 1.4.0
//
// Reads GoReleaser's build output from dist/ and writes dist/npm/<name>/. npm,
// pnpm and yarn install only the package whose os/cpu fields match the host,
// and they do it without an install script.
//
// Keep the package names in step with PLATFORM_PACKAGES in
// installer/src/resolveBinary.js, and the `build` prefixes with the extra_files
// globs in .goreleaser.yaml. A prefix, not a directory name, because GoReleaser
// appends a microarchitecture suffix (_v1, _v8.0) that has changed before.

const { chmodSync, copyFileSync, existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } = require("node:fs");
const { join } = require("node:path");

const ROOT = join(__dirname, "..");
const DIST = join(ROOT, "dist");

const TARGETS = [
  { pkg: "@noetive/riffle-darwin-arm64", build: "riffle_darwin_arm64", os: "darwin", cpu: "arm64", bin: "riffle" },
  { pkg: "@noetive/riffle-darwin-x64", build: "riffle_darwin_amd64", os: "darwin", cpu: "x64", bin: "riffle" },
  { pkg: "@noetive/riffle-linux-arm64", build: "riffle_linux_arm64", os: "linux", cpu: "arm64", bin: "riffle" },
  { pkg: "@noetive/riffle-linux-x64", build: "riffle_linux_amd64", os: "linux", cpu: "x64", bin: "riffle" },
  { pkg: "@noetive/riffle-win32-x64", build: "riffle_windows_amd64", os: "win32", cpu: "x64", bin: "riffle.exe" },
];

// Exactly one match: zero means the build skipped a platform, two means
// GoReleaser built several microarchitecture levels and the package would be a
// coin toss between them.
function buildDir(prefix) {
  const matches = readdirSync(DIST, { withFileTypes: true })
    .filter((entry) => entry.isDirectory() && entry.name.startsWith(prefix))
    .map((entry) => entry.name);
  if (matches.length !== 1) {
    console.error(`expected exactly one dist/ directory for ${prefix}, found ${matches.length}: ${matches.join(", ")}`);
    process.exit(1);
  }
  return matches[0];
}

function main() {
  const version = process.argv[2];
  if (!version) {
    console.error("usage: build-platform-packages.js <version>");
    process.exit(2);
  }
  if (!existsSync(DIST)) {
    console.error(`${DIST} does not exist; run goreleaser before building the platform packages`);
    process.exit(1);
  }

  const wrapperPath = join(ROOT, "installer", "package.json");
  const wrapper = JSON.parse(readFileSync(wrapperPath, "utf8"));
  if (wrapper.version !== version) {
    console.error(`installer/package.json is at ${wrapper.version}, not ${version}; run scripts/stamp-version.js first`);
    process.exit(1);
  }

  for (const target of TARGETS) {
    const source = join(DIST, buildDir(target.build), target.bin);
    if (!existsSync(source)) {
      console.error(`missing release binary ${source}; the release build did not produce every platform`);
      process.exit(1);
    }

    const dir = join(DIST, "npm", target.pkg.replace("@noetive/", ""));
    mkdirSync(join(dir, "bin"), { recursive: true });
    const binary = join(dir, "bin", target.bin);
    copyFileSync(source, binary);
    chmodSync(binary, 0o755);

    const manifest = {
      name: target.pkg,
      version,
      description: `riffle binary for ${target.os}-${target.cpu}`,
      license: "ISC",
      repository: { type: "git", url: "git+https://github.com/noetive/riffle.git" },
      os: [target.os],
      cpu: [target.cpu],
      files: ["bin/"],
      // Yarn PnP serves package files out of a zip, where a binary cannot be
      // executed. This asks it to unpack the package to disk; other package
      // managers ignore it.
      preferUnplugged: true,
    };
    writeFileSync(join(dir, "package.json"), `${JSON.stringify(manifest, null, 2)}\n`);
    writeFileSync(
      join(dir, "README.md"),
      `# ${target.pkg}\n\nPlatform binary for [@noetive/riffle](https://www.npmjs.com/package/@noetive/riffle).\nInstalled automatically on ${target.os}-${target.cpu}; do not depend on it directly.\n`,
    );
    console.log(`built ${target.pkg}@${version}`);
  }

  wrapper.optionalDependencies = Object.fromEntries(TARGETS.map((t) => [t.pkg, version]));
  writeFileSync(wrapperPath, `${JSON.stringify(wrapper, null, 2)}\n`);
  console.log(`pinned the wrapper to ${TARGETS.length} platform packages at ${version}`);
}

main();
