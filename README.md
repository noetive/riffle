<div align="center">

# Riffle

### Your agent uses the web as text, not screenshots.

**Pages as text. Only what changed comes back. Several steps per call. Fully interactive, without a mouse or keyboard.**

[![npm](https://img.shields.io/npm/v/@noetive/riffle?style=flat-square&color=cb3837&label=npm)](https://www.npmjs.com/package/@noetive/riffle)
[![CI](https://img.shields.io/github/actions/workflow/status/noetive/riffle/ci.yml?branch=main&style=flat-square&label=CI)](https://github.com/noetive/riffle/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/noetive/riffle?style=flat-square&logo=go&logoColor=white&color=00add8)](go.mod)
[![MCP server](https://img.shields.io/badge/MCP-server-5c4ee5?style=flat-square)](#install)
[![License: ISC](https://img.shields.io/badge/license-ISC-f4c430?style=flat-square)](LICENSE)

**[Why Riffle](#why-riffle) · [Install](#install) · [Use](#use) · [Stay signed in](#stay-signed-in) · [Safe by default](#safe-by-default) · [Releases](https://github.com/noetive/riffle/releases)**

<code>npx @noetive/riffle init</code>

<sub>Claude Code · Cursor · GitHub Copilot · Kiro · Antigravity · any MCP client</sub>

</div>

---

Riffle is an MCP server and CLI. It runs Chrome with the page's JavaScript and hands your agent each page as text it can read and act on.

## Why Riffle

### Your agent sees what a person sees

What is covered, hidden, primary, disabled, struck through or truncated is written on the line, in words. No HTML, no CSS, no images: Riffle turns the page into short lines of text, so your agent reads these facts instead of guessing them from markup or pixels.

```
modal d1 "Cookie preferences" covers=page
  text "We use cookies."
  button b1 "Accept all" primary
  button b2 "Reject"
main covered-by=d1
  h1 "Your cart"
  list
    item "Trail shoe 42" "€89" strike "€69" red | "Qty" | spinbutton f1 "Qty" ="1" | button b3 "Remove"
  button b4 "Checkout"
```

The cart sits behind a cookie dialog, the old price is struck through and the new one is red. `covered-by=d1` tells your agent the checkout button sits behind the dialog.

### Context that lasts the whole task

After the first view, every reply carries only what changed. Click "Reject" on the page above and this is the whole reply:

```
- d1
- text "We use cookies."
- b1
- b2
~ main -covered-by=d1
```

Every view also takes a token budget, so a long page costs what you set: `view interactive budget=800`.

### Fewer turns, less waiting

Your agent writes a program, one step per line, and Riffle runs all of it in one call. A whole sign-in is one turn:

```
goto https://shop.example/login
try click "Reject"
fill "Email" "ralph@example.com"
fill "Password" $secret:shop
click "Sign in"
expect url ~ /account
view interactive budget=800
```

After each step Riffle waits until the page has answered and gone quiet instead of sleeping for a fixed time: about a second after a click, a few seconds after a page loads. The program stops at the first step that fails and says which step and why.

### No mouse, no keyboard, no screenshots

Your agent names what to act on by the words on it or by a ref from a view: `click "Reject"`, `fill "Email" "ralph@example.com"`, `click b4`. Your agent never steers a pointer, aims at coordinates or reads an image. `fill` sets a whole field in one step, and `press Enter` names the key. When a step can't happen, the reply says what is in the way:

```
failed line 1: click "Checkout": blocked: b4 covered-by d1 "Cookie preferences"
```

Your agent's next program closes the dialog first.

### Instead of

| What your agent uses now | What it costs | With Riffle |
| --- | --- | --- |
| Screenshots and clicks at coordinates | An image and a turn for every action, and guesses from pixels | Text, and several steps per call |
| A page snapshot or raw HTML after every action | The whole page again in your agent's context, every time | Only what changed |
| Fetching a page as Markdown | It can read, but it can't click, fill in or sign in | A real browser: click, fill in, sign in |

## Install

```bash
npx @noetive/riffle init --client claude-code
```

Other editors: `--client cursor`, `copilot`, `kiro`, `antigravity`. Run `init` with no `--client` and it configures the editor it finds. `--dry-run` shows the change without writing it. `--keep-state NAME` keeps the agent signed in between runs; see [Stay signed in](#stay-signed-in).

Any MCP client can use this entry:

```json
{
  "mcpServers": {
    "riffle": { "command": "npx", "args": ["-y", "@noetive/riffle", "mcp"] }
  }
}
```

On native Windows, run npx through cmd: `"command": "cmd", "args": ["/c", "npx", "-y", "@noetive/riffle", "mcp"]`. `init` writes this form for you.

Or install the binary: `go install github.com/noetive/riffle/cmd/riffle@latest`, or take one from the [releases](https://github.com/noetive/riffle/releases).

Riffle needs Chrome or Chromium on the machine. Check with:

```bash
riffle doctor
```

Riffle runs its own throwaway Chrome. It never touches your saved passwords or keychain, and it cleans up after itself when it stops.

## Use

Your agent gets two tools. `browser_run` takes a program, one step per line, like the sign-in under [Fewer turns, less waiting](#fewer-turns-less-waiting). `browser_view` reads the page.

Not sure of the syntax? Ask for `view help`, or run `riffle grammar`.

Steps: `goto back forward click dblclick fill select check press hover scroll upload wait expect dialog try view eval`.
Views: `outline`, `interactive`, `read`, `table REF`, `find "text"`, `expand REF`, `net`, `unseen`.

From a shell:

```bash
riffle run  < program.txt      # run a program
riffle view interactive        # read the current page
riffle close                   # end the session and its browser
```

Each editor's agent has a browser of its own, so two windows never drive the same page. Its MCP server names its session on stderr at start; `riffle view -s NAME` shows that agent's page. Entries that pass the same `-s NAME`, as in `riffle mcp -s shared`, share one browser.

Riffle keeps browsers from piling up on your machine. At most four run at once, however many agents use Riffle (`riffle serve -max-browsers N` to change it). An agent that needs another waits for one to end, then is told which sessions hold them. A session unused for 15 minutes ends, and its next use starts on a blank page and says so. An editor's session ends when the editor stops its MCP server, and a Riffle with nothing to do exits after 30 minutes.

### Stay signed in

Name a session in your editor's entry and Riffle keeps its sign-ins. The agent signs in once, and the next time the editor starts it is still signed in.

```bash
npx @noetive/riffle init --client claude-code --keep-state shop
```

or, by hand:

```json
{ "mcpServers": { "riffle": { "command": "npx", "args": ["-y", "@noetive/riffle", "mcp", "-keep-state", "shop"] } } }
```

- **What survives.** Cookies and site storage are saved after every call, so they outlast the editor restarting, Riffle stopping and the browser crashing. Starting again sends nothing to any site: the first request is the agent's own.
- **Where it is kept.** At start Riffle names the file in its MCP server log. Only you can read it, but whoever can read it can use those sign-ins: keep it out of shared backups and synced folders. To sign the session out of everything, close the editors that use the name and delete the file.
- **One name, one browser.** Editors whose entries use the same name share one browser and one set of sign-ins. Give each project its own name to keep them apart. A name is lower case letters, digits, `-` and `_`.
- **Limits still hold.** A session started with `-allow-origin` gets back only the sign-ins of the sites it allows, and none for private addresses unless `-allow-private` is set. What a limit leaves out stays kept for a session allowed it.
- **What is not kept.** Storage is kept for the site the agent is on when each call ends, not for a site it only passed through within a call, nor for a frame from another site. Storage is kept for up to 50 sites; the one used longest ago goes first. A site whose storage cannot be put back is reported to the agent as `event unrestored`, and the session starts without it; it stays kept.

## What you can do with it

- **Pull a table out of a page.** `table REF` returns rows as TSV, whatever the markup.
- **See what the page calls.** `view net` lists the fetch and XHR requests behind it.
- **Test your own web app.** Drive localhost the way a user would, with real page JavaScript running. Start Riffle with `-allow-private`, for example `riffle mcp -allow-private` in your editor's entry, so it may reach local addresses.
- **Stay signed in between runs.** Sign in once with `-keep-state NAME`, and the agent is still signed in the next time the editor starts. See [Stay signed in](#stay-signed-in).

Pages run in real time, as in any browser, and keep running while your agent thinks: a countdown, an expiring sign-in or a panel that opens later moves on between calls, and the next reply tells what changed.

Riffle also says so when a step did not do what it asked, such as a form that was refused, so the agent can fix it and go on.

## Safe by default

- Secrets are written as `$secret:name`, tied to one origin, and never shown back to the agent.
- Text a person couldn't see on the page is counted, and shown only when you ask, inside a marker that says it's page data.
- A control whose hidden label contradicts the words on it is flagged `name-differs`.
- Page text is data. Replies quote it, so a page can't pass it off as your instructions.
- Local and private network addresses are off until you turn them on with `-allow-private`, so a page cannot send the agent into your network or a cloud metadata service.
- Uploads and `eval` are off until you turn them on. `-allow-origin` and `-upload-dir` limit where a session can go and what it can send. A Riffle that restarts after a crash keeps the limits it was given.
- Sign-ins end with their session, unless you keep them with `-keep-state NAME`. Kept sign-ins are in a file only you can read; delete it to forget them. A session gets back only the sign-ins its limits allow.

## What it isn't

- Not a screenshot tool. If the answer is only in the pixels, Riffle can't give it to you.
- Not a way around bot protection or CAPTCHAs. It doesn't try. Every request says it comes from Riffle: the user agent ends in `Riffle/<version>`.
- Not a test framework for visual regressions. Use a tool built for that.
- Chrome and Chromium only. Content inside iframes is reported as `content-not-shown`, not read.
- Links that open a mail or phone app do nothing, and a new window opens in the same tab, so flows that need a pop-up do not work.

## Works with the rest of Noetive

Riffle runs alone. It also pairs with another Noetive service:

- **[Semantik](https://noetive.io)** is a semantic message broker. An agent that reads a page can publish what it found, and its peers find it by meaning, with no topic names to agree on.

## Develop

```bash
make build
make test
make lint
go test -tags integration ./...   # drives a real Chrome
```

See [CONTRIBUTING.md](CONTRIBUTING.md). Report security issues as described in [SECURITY.md](SECURITY.md).

## License

ISC. Copyright (c) 2026 Noetive.io
