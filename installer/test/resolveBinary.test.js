"use strict";

const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { join } = require("node:path");
const { test } = require("node:test");

const { PLATFORM_PACKAGES, assetName, assetUrl, binaryName, platformPackage, resolveBinary } = require("../src/resolveBinary");
const { parseChecksums, verifyDownload } = require("../src/verify");
const { put, sandbox } = require("./helpers");

test("every platform resolves to a riffle package and a release asset", () => {
  assert.deepEqual(Object.keys(PLATFORM_PACKAGES).sort(), ["darwin-arm64", "darwin-x64", "linux-arm64", "linux-x64", "win32-x64"]);
  for (const [key, pkg] of Object.entries(PLATFORM_PACKAGES)) assert.equal(pkg, `@noetive/riffle-${key}`);
  assert.deepEqual(
    Object.keys(PLATFORM_PACKAGES).map((k) => assetName(k)).sort(),
    ["riffle-darwin-arm64", "riffle-darwin-x64", "riffle-linux-arm64", "riffle-linux-x64", "riffle-win32-x64.exe"],
  );
  assert.equal(assetUrl("1.2.3", "linux-x64"), "https://github.com/noetive/riffle/releases/download/v1.2.3/riffle-linux-x64");
});

test("the platform packages match what the build script produces", () => {
  const script = readFileSync(join(__dirname, "..", "..", "scripts", "build-platform-packages.js"), "utf8");
  for (const pkg of Object.values(PLATFORM_PACKAGES)) assert.ok(script.includes(`"${pkg}"`), pkg);
});

test("the platform package is preferred over the downloaded fallback", (t) => {
  const { root } = sandbox(t);
  const pkgDir = join(root, "pkg");
  const fallbackDir = join(root, "fallback");
  put(join(pkgDir, "bin", "riffle"), "");
  put(join(fallbackDir, "riffle"), "");
  const path = resolveBinary({ platform: "linux", arch: "x64", resolvePackage: () => pkgDir, fallbackDir });
  assert.equal(path, join(pkgDir, "bin", "riffle"));
});

test("the fallback is used when the package did not install", (t) => {
  const { root } = sandbox(t);
  put(join(root, "riffle.exe"), "");
  const path = resolveBinary({ platform: "win32", arch: "x64", resolvePackage: () => undefined, fallbackDir: root });
  assert.equal(path, join(root, "riffle.exe"));
  assert.equal(binaryName("win32"), "riffle.exe");
});

test("a missing binary explains how to fix it", () => {
  assert.throws(
    () => resolveBinary({ platform: "linux", arch: "x64", resolvePackage: () => undefined, fileExists: () => false }),
    /npm rebuild @noetive\/riffle/,
  );
});

test("an unsupported platform lists the supported ones", () => {
  assert.throws(() => platformPackage("freebsd-x64"), /not a supported platform.*darwin-arm64/);
});

test("a download is accepted only when its checksum is published and matches", (t) => {
  const { root } = sandbox(t);
  const file = join(root, "riffle");
  put(file, "binary");
  const sum = require("node:crypto").createHash("sha256").update("binary").digest("hex");

  assert.doesNotThrow(() => verifyDownload(file, "riffle-linux-x64", `${sum}  riffle-linux-x64\n`));
  assert.throws(() => verifyDownload(file, "riffle-linux-x64", `${"0".repeat(64)}  riffle-linux-x64\n`), /mismatch/);
  assert.throws(() => verifyDownload(file, "riffle-linux-x64", `${sum}  other\n`), /no published checksum/);
  assert.equal(parseChecksums("junk\n").size, 0);
});
