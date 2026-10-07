#!/usr/bin/env node
"use strict";

// Fallback binary download. The primary path is optionalDependencies, which
// works even when install scripts are blocked. This runs only when that did not
// land, is best-effort (a failure must not fail the install), and always
// verifies the download against the release's checksums.txt before making it
// executable.

const { chmodSync, mkdirSync, renameSync, writeFileSync } = require("node:fs");
const { join } = require("node:path");

async function fetchOk(url) {
  const response = await fetch(url, { redirect: "follow" });
  if (!response.ok) throw new Error(`GET ${url} returned ${response.status}`);
  return response;
}

async function download({ assetName, assetUrl, binaryName, checksumsUrl, hostKey }, verifyDownload) {
  const version = require("./package.json").version;
  const key = hostKey();
  const asset = assetName(key);

  const checksums = await (await fetchOk(checksumsUrl(version))).text();
  const bytes = Buffer.from(await (await fetchOk(assetUrl(version, key))).arrayBuffer());

  const dir = join(__dirname, "bin");
  mkdirSync(dir, { recursive: true });

  // Verified under a temporary name so a bad download is never at the path the
  // wrapper executes.
  const staged = join(dir, `.${asset}.partial`);
  writeFileSync(staged, bytes);
  verifyDownload(staged, asset, checksums);

  const target = join(dir, binaryName());
  renameSync(staged, target);
  chmodSync(target, 0o755);
}

function main() {
  let resolver;
  let verifyDownload;
  try {
    resolver = require("./src/resolveBinary.js");
    ({ verifyDownload } = require("./src/verify.js"));
  } catch {
    return;
  }

  try {
    resolver.resolveBinary();
    return;
  } catch {
    // Fall through to the download.
  }

  download(resolver, verifyDownload).catch((err) => {
    console.error(`riffle: could not pre-fetch the binary (${err.message}).`);
    console.error("riffle: install it with `npm rebuild @noetive/riffle`.");
  });
}

main();
