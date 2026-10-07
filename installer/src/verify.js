"use strict";

const { createHash } = require("node:crypto");
const { readFileSync } = require("node:fs");

const checksumOf = (path) => createHash("sha256").update(readFileSync(path)).digest("hex");

// Reads a release's checksums.txt: `<hex>  <filename>` per line. Malformed
// lines are skipped so a blank line or header cannot fail an install.
function parseChecksums(text) {
  const sums = new Map();
  for (const line of text.split("\n")) {
    const match = /^([0-9a-fA-F]{64})\s+\*?(\S+)$/.exec(line.trim());
    if (match) sums.set(match[2], match[1].toLowerCase());
  }
  return sums;
}

// Throws unless the file matches the checksum published for assetName. An
// unlisted asset fails: treating "no checksum" as a pass would let anyone who
// can serve the download bypass the check.
function verifyDownload(path, assetName, checksumsText) {
  const published = parseChecksums(checksumsText).get(assetName);
  if (!published) {
    throw new Error(`no published checksum for ${assetName}; refusing to install an unverified binary`);
  }
  const actual = checksumOf(path);
  if (actual !== published) {
    throw new Error(`checksum mismatch for ${assetName}: expected ${published}, got ${actual}; it has not been installed`);
  }
}

module.exports = { checksumOf, parseChecksums, verifyDownload };
