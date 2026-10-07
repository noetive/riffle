"use strict";

const { copyFileSync, existsSync, mkdirSync, readFileSync, renameSync, unlinkSync, writeFileSync } = require("node:fs");
const { dirname, join } = require("node:path");

const { applyEdits, modify, parse, printParseErrorCode } = require("jsonc-parser");

const FORMATTING = { insertSpaces: true, tabSize: 2, eol: "\n" };

// A missing file and an empty file are the same: nothing to preserve.
function read(path) {
  if (!existsSync(path)) return "{}";
  const text = readFileSync(path, "utf8");
  return text.trim() === "" ? "{}" : text;
}

// Tolerates comments and trailing commas, which editors write. A file that does
// not parse is an error, never an empty object: treating a corrupt config as
// empty would let the next write discard every server the user configured.
function parseObject(text, path) {
  const errors = [];
  const value = parse(text, errors, { allowTrailingComma: true, disallowComments: false });
  if (errors.length > 0) {
    throw new Error(
      `${path} is not valid JSON (${printParseErrorCode(errors[0].error)} at offset ${errors[0].offset}); fix or move it and re-run`,
    );
  }
  if (value === undefined || value === null) return {};
  if (typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${path} must contain a JSON object at the top level`);
  }
  return value;
}

// Range edits, not re-serialisation: comments, key order and indentation
// elsewhere in the file survive.
function edit(text, path, value) {
  const next = applyEdits(text, modify(text, path, value, { formattingOptions: FORMATTING }));
  return next.endsWith("\n") ? next : `${next}\n`;
}

const setEntry = (text, path, value) => edit(text, path, value);
const removeEntry = (text, path) => edit(text, path, undefined);

const backupPath = (path) => `${path}.riffle.bak`;

// Atomic replace after a single rolling backup. Returns the backup path, or
// undefined when there was no prior file.
function write(path, text) {
  mkdirSync(dirname(path), { recursive: true });
  let backup;
  if (existsSync(path)) {
    backup = backupPath(path);
    copyFileSync(path, backup);
  }
  const temp = join(dirname(path), `.riffle-${process.pid}.tmp`);
  writeFileSync(temp, text, { encoding: "utf8", mode: 0o600 });
  renameSync(temp, path);
  return backup;
}

function restore(path, backup) {
  copyFileSync(backup, path);
  unlinkSync(backup);
}

// The changed region only: matching head and tail lines are elided.
function diff(before, after, path) {
  const a = before.split("\n");
  const b = after.split("\n");
  let head = 0;
  while (head < a.length && head < b.length && a[head] === b[head]) head += 1;
  let tail = 0;
  while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail += 1;

  const removed = a.slice(head, a.length - tail);
  const added = b.slice(head, b.length - tail);
  if (removed.length === 0 && added.length === 0) return `${path}: no change`;

  return [
    `--- ${path}`,
    `+++ ${path}`,
    `@@ line ${head + 1} @@`,
    ...removed.map((line) => `-${line}`),
    ...added.map((line) => `+${line}`),
  ].join("\n");
}

module.exports = { backupPath, diff, parseObject, read, removeEntry, restore, setEntry, write };
