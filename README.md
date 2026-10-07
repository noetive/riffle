# Riffle

**A browser your agent can read.** Several steps per call. Only what changed comes back.

Riffle is an MCP server and CLI that lets an AI agent use real websites without screenshots.

[![ci](https://github.com/noetive/riffle/actions/workflows/ci.yml/badge.svg)](https://github.com/noetive/riffle/actions/workflows/ci.yml)

## The problem

Your browser agent spends a turn and an image on every click. Or it re-reads the whole page after every action, and still can't tell that the "Checkout" button is behind a cookie dialog, that the price is struck through, or that a field is disabled.

Riffle gives the agent what the page means:

```
modal d1 "Cookie preferences" covers=page
  button b1 "Accept all" primary
  button b2 "Reject"
main covered-by=d1
  h1 "Your cart"
  item "Trail shoe 42" "€89" strike "€69" red | qty f1=1 | button b3 "Remove"
  button b5 "Checkout" primary
```

Hidden, covered, primary, disabled, struck through, truncated. These are facts on the nodes, so the agent reads them instead of guessing from pixels.

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

Or install the binary: `go install github.com/noetive/riffle/cmd/riffle@latest`, or take one from the [releases](https://github.com/noetive/riffle/releases).

Riffle needs Chrome or Chromium on the machine. Check with:

```bash
riffle doctor
```

Riffle runs its own throwaway Chrome. It never touches your saved passwords or keychain, and it cleans up after itself when it stops.

## Use

Your agent gets two tools. `browser_run` takes a program, one step per line. `browser_view` reads the page.

```
goto https://shop.example/cart
click "Reject"
fill "Email" "ralph@example.com"
fill "Password" $secret:shop
click "Sign in"
expect url ~ /account
view interactive budget=800
```

The reply is what changed, not the page again:

```
- d1
~ main -covered-by=d1
```

If a step fails, the program stops there and says which step and why. A click on something covered by a dialog comes back as `blocked: b5 covered-by d1 "Cookie preferences"`, so the next program can close the dialog first.

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

- **Log in and finish a flow.** Fill, click, check the result, in one call.
- **Read a page at a size you choose.** Every view takes a token budget, so a long page costs what you decide.
- **Pull a table out of a page.** `table REF` returns rows as TSV, whatever the markup.
- **See what the page calls.** `view net` lists the fetch and XHR requests behind it.
- **Test your own web app.** Drive localhost the way a user would, with real page JavaScript running. Start Riffle with `-allow-private`, for example `riffle mcp -allow-private` in your editor's entry, so it may reach local addresses.
- **Stay signed in between runs.** Sign in once with `-keep-state NAME`, and the agent is still signed in the next time the editor starts. See [Stay signed in](#stay-signed-in).

Pages run in real time, as in any browser, and keep running while your agent thinks: a countdown, an expiring sign-in or a panel that opens later moves on between calls, and the next reply tells what changed. After each step Riffle waits until the page has answered and gone quiet: about a second after a click or a keystroke, a few seconds after a page loads, and longer for a page still loading.

Riffle also says so when a step did not do what it asked, such as a form that was refused or a click that a dialog blocked, so the agent can fix it and go on.

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
