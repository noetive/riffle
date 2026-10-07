/**
 * assert-entry reads an editor's MCP config back and checks the riffle entry.
 *
 *     node .github/scripts/assert-entry.mjs <config> <topLevelKey> <present|absent>
 *
 * Used by .github/workflows/install-clients.yml. It reads the file the editor
 * reads, not the installer's own output, because the installer reporting
 * success is the claim under test.
 *
 * The workflow seeds the config with an unrelated server named "other" and an
 * unrelated top-level key named "unrelated" before the first install. Both must
 * survive every install and remove, so each mode checks them as well.
 */
import { readFileSync } from "node:fs";
import { isDeepStrictEqual } from "node:util";

const [path, topLevelKey, mode] = process.argv.slice(2);
if (!path || !topLevelKey || !["present", "absent"].includes(mode ?? "")) {
  console.error("usage: assert-entry.mjs <config> <topLevelKey> <present|absent>");
  process.exit(2);
}

const fail = (message) => {
  console.error(`${path}: ${message}`);
  console.error(readFileSync(path, "utf8").slice(0, 2000));
  process.exit(1);
};

const config = JSON.parse(readFileSync(path, "utf8"));
const servers = config[topLevelKey];
if (servers === null || typeof servers !== "object") fail(`no "${topLevelKey}" object`);

if (!isDeepStrictEqual(servers.other, { command: "echo", args: ["unrelated"] })) fail('the unrelated "other" server was not preserved');
if (config.unrelated !== true) fail('the unrelated top-level key was not preserved');

if (mode === "absent") {
  if ("riffle" in servers) fail("the riffle entry is still present");
} else {
  const entry = servers.riffle;
  if (entry === undefined) fail("the riffle entry is missing");
  if (entry.command !== "npx") fail(`command is ${JSON.stringify(entry.command)}, expected "npx"`);
  if (!isDeepStrictEqual(entry.args, ["-y", "@noetive/riffle", "mcp"])) fail(`args are ${JSON.stringify(entry.args)}`);
}

console.log(`${path}: riffle is ${mode}, unrelated entries preserved`);
