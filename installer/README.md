# @noetive/riffle

Your agent uses the web as text, not screenshots. This package installs [Riffle](https://github.com/noetive/riffle) as an MCP server in your AI editor.

```bash
npx @noetive/riffle init --client claude-code
```

Clients: `claude-code`, `cursor`, `copilot`, `kiro`, `antigravity`. Leave out `--client` to configure every editor found.

| Command | Does |
| --- | --- |
| `init` | Adds a `riffle` entry to the editor's MCP config and installs the Riffle skill where the editor supports skills. Only the `riffle` entry is touched, and the previous file is saved next to it as `*.riffle.bak`. |
| `remove --client ID` | Removes the `riffle` entry and the skill. |
| `list` | Shows which editors have the entry. |
| `doctor` | Runs `riffle doctor` (browser found, starts, scripts run), then shows which editors have the entry. |

Flags: `--scope user|project`, `--chrome PATH` (sets `RIFFLE_CHROME`), `--keep-state NAME`, `--yes`, `--dry-run`.

`--keep-state NAME` keeps the agent signed in between runs: the entry starts Riffle with `-keep-state NAME`, and the session's cookies and site storage are kept in a file only you can read. A name is lower case letters, digits, `-` and `_`; editors given the same name share one browser and its sign-ins. See [Stay signed in](https://github.com/noetive/riffle#stay-signed-in).

Any other command, such as `mcp`, runs the Riffle binary. Riffle needs Chrome or Chromium on the machine.

For an editor not listed, such as Codex, add this server by hand:

```json
{ "mcpServers": { "riffle": { "command": "npx", "args": ["-y", "@noetive/riffle", "mcp"] } } }
```

ISC. Copyright (c) 2026 Noetive.io
