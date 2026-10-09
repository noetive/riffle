"use strict";

const { existsSync, readFileSync, readdirSync, rmSync } = require("node:fs");
const { join, relative, sep } = require("node:path");
const { isDeepStrictEqual } = require("node:util");

const { SERVER_NAME, buildEntry, configPath, isInstalled, skillsPath } = require("./clients");
const configFile = require("./configFile");

function entryAt(target, topLevelKey) {
  return entryIn(configFile.parseObject(configFile.read(target), target), topLevelKey);
}

function entryIn(doc, topLevelKey) {
  const servers = doc[topLevelKey];
  if (!servers || typeof servers !== "object" || Array.isArray(servers)) return undefined;
  const entry = servers[SERVER_NAME];
  return entry && typeof entry === "object" && !Array.isArray(entry) ? entry : undefined;
}

// Sets the `riffle` entry and nothing else. Returns { target, changed, diff?,
// backup? }. With dryRun nothing is written.
function install(spec, scope, ctx, options = {}) {
  const target = configPath(spec, scope, ctx);
  const before = configFile.read(target);
  const doc = configFile.parseObject(before, target); // a corrupt file is a refusal, not a clobber

  // Compared by value: the editor may have saved the file in its own format,
  // and re-rendering a matching entry would rewrite it for nothing.
  const wanted = buildEntry(spec, options, ctx.platform);
  if (isDeepStrictEqual(entryIn(doc, spec.topLevelKey), wanted)) return { target, changed: false };

  const after = configFile.setEntry(before, [spec.topLevelKey, SERVER_NAME], wanted);
  if (after === before) return { target, changed: false };
  if (options.dryRun) return { target, changed: true, diff: configFile.diff(before, after, target) };

  const backup = configFile.write(target, after);

  // Read back what was written: a write that produced no usable entry is worse
  // than a failed write, because the editor would silently have no browser.
  const entry = entryAt(target, spec.topLevelKey);
  if (!entry || typeof entry.command !== "string" || !Array.isArray(entry.args)) {
    if (backup) configFile.restore(target, backup);
    throw new Error(`${target} was written but holds no usable ${SERVER_NAME} entry; the previous file was restored`);
  }
  return backup ? { target, changed: true, backup } : { target, changed: true };
}

function remove(spec, scope, ctx, options = {}) {
  const target = configPath(spec, scope, ctx);
  if (!existsSync(target)) return { target, changed: false };

  const before = configFile.read(target);
  configFile.parseObject(before, target);
  if (!entryAt(target, spec.topLevelKey)) return { target, changed: false };

  const after = configFile.removeEntry(before, [spec.topLevelKey, SERVER_NAME]);
  if (options.dryRun) return { target, changed: true, diff: configFile.diff(before, after, target) };
  const backup = configFile.write(target, after);
  return backup ? { target, changed: true, backup } : { target, changed: true };
}

// { target, installed, configured: "yes" | "no", detail? }
function status(spec, scope, ctx) {
  const target = configPath(spec, scope, ctx);
  const installed = isInstalled(spec, ctx, existsSync);
  if (!existsSync(target)) return { target, installed, configured: "no" };
  try {
    const entry = entryAt(target, spec.topLevelKey);
    if (!entry) return { target, installed, configured: "no" };
    const args = Array.isArray(entry.args) ? entry.args.join(" ") : "";
    return { target, installed, configured: "yes", detail: `${entry.command ?? ""} ${args}`.trim() };
  } catch (err) {
    return { target, installed, configured: "no", detail: err.message };
  }
}

function walk(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? walk(join(dir, e.name)) : [join(dir, e.name)]));
}

// Skills shipped in this package: skills/<name>/SKILL.md plus any siblings.
function bundledSkills(root = join(__dirname, "..", "skills")) {
  if (!existsSync(root)) return [];
  return readdirSync(root, { withFileTypes: true })
    .filter((e) => e.isDirectory() && existsSync(join(root, e.name, "SKILL.md")))
    .map((e) => ({
      name: e.name,
      files: walk(join(root, e.name)).map((p) => ({
        relative: relative(join(root, e.name), p).split(sep).join("/"),
        contents: readFileSync(p, "utf8"),
      })),
    }));
}

// Identical files are left alone so a re-run does not replace the user's backup
// with a copy of the same content.
function installSkills(spec, scope, ctx, skills, options = {}) {
  const target = skillsPath(spec, scope, ctx);
  if (!target) return { written: [], unchanged: [], unsupported: `${spec.displayName} has no skills directory, so none were installed.` };

  const written = [];
  const unchanged = [];
  for (const skill of skills) {
    for (const file of skill.files) {
      const path = join(target, skill.name, ...file.relative.split("/"));
      if (existsSync(path) && readFileSync(path, "utf8") === file.contents) {
        unchanged.push(path);
        continue;
      }
      written.push(path);
      if (!options.dryRun) configFile.write(path, file.contents);
    }
  }
  return { target, written, unchanged };
}

// Only directories this package ships, and only when a SKILL.md is present: a
// user's own skill beside ours is not ours to delete.
function removeSkills(spec, scope, ctx, skills, options = {}) {
  const target = skillsPath(spec, scope, ctx);
  const removed = [];
  if (!target) return { removed };
  for (const skill of skills) {
    const dir = join(target, skill.name);
    if (!existsSync(join(dir, "SKILL.md"))) continue;
    removed.push(dir);
    if (!options.dryRun) rmSync(dir, { recursive: true, force: true });
  }
  return { removed };
}

module.exports = { bundledSkills, install, installSkills, remove, removeSkills, status };
