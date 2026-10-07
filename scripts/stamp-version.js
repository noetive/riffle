#!/usr/bin/env node
"use strict";

// Stamps one version into installer/package.json and its lockfile.
//
//     node scripts/stamp-version.js 1.4.0
//
// Idempotent: the release workflow runs it on a tree that should already carry
// the tag's version, so a correct release makes it a no-op. The platform pins
// are not written here; scripts/build-platform-packages.js writes them just
// before publish, because a committed pin can only name a version that does not
// exist yet and npm ci rejects the lockfile once it does.

const { readFileSync, writeFileSync } = require("node:fs");
const { join } = require("node:path");

const ROOT = join(__dirname, "..");

function stampJson(path, mutate) {
  const doc = JSON.parse(readFileSync(path, "utf8"));
  mutate(doc);
  writeFileSync(path, `${JSON.stringify(doc, null, 2)}\n`);
}

function main() {
  const version = process.argv[2];
  if (!version || !/^\d+\.\d+\.\d+(-[\w.]+)?$/.test(version)) {
    console.error("usage: stamp-version.js <semver>  (e.g. 1.4.0)");
    process.exit(2);
  }

  stampJson(join(ROOT, "installer", "package.json"), (doc) => {
    doc.version = version;
  });
  stampJson(join(ROOT, "installer", "package-lock.json"), (doc) => {
    doc.version = version;
    if (!doc.packages || !doc.packages[""]) {
      console.error("installer/package-lock.json has no root package entry");
      process.exit(1);
    }
    doc.packages[""].version = version;
  });
  console.log(`stamped ${version}`);
}

main();
