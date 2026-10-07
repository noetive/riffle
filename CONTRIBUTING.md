# Contributing

## Getting set up

```sh
make build
make test
make lint
go test -count=1 -tags integration ./...
```

The integration tests drive a real browser and need Google Chrome (or Chromium) installed. Without the `integration` tag they are not compiled. `make lint` needs [golangci-lint](https://golangci-lint.run) v2.

`make build` and `make test` also enable the repository's git hooks. Before each commit the hook checks staged files for credentials and formats them: Go with gofmt, the installer's JavaScript with Biome. A file whose every change is staged is fixed and staged again; one with unstaged changes too is left alone and the commit refused. It then runs golangci-lint, the unit tests and `govulncheck` when Go files changed, shellcheck on shell scripts, and the installer tests when `installer/` changed. A tool you don't have is named and skipped; CI runs all of it either way. Please don't bypass the hook.

The installer is a Node package in `installer/`. It needs Node 22 or newer:

```sh
cd installer && npm ci && npm test
```

## Repository layout

| Path | Purpose |
| --- | --- |
| `cmd/riffle` | The command: serve, run, view, doctor, version, mcp |
| `internal/chromium` | Talking to the browser |
| `internal/facts` | Turning a page into what a reader can see |
| `internal/view` | Rendering what changed on a page |
| `internal/snapshot` | Page state captured between steps |
| `internal/program` | Parsing the programs agents send |
| `internal/engine` | Running a program against a session |
| `internal/session` | Browser sessions and their lifetime |
| `internal/daemon` | The background process, secrets and navigation policy |
| `internal/mcpserver` | The MCP tool surface |
| `internal/audit` | The record of what was done |
| `internal/wire` | Messages between the command and the daemon |
| `installer/` | The npm wrapper and per-platform packages |

## Tests

Tests describe behaviour and properties, not implementation. Name a test for what is true, and prefer a property over a list of examples.

Tests must not depend on the date. Derive timestamps from the clock, or inject one, so a test that passes today passes on any later run.

A test is worth having only if it fails when the code is wrong. Check new tests by mutation testing the code they cover with [github.com/gurre/mutest](https://github.com/gurre/mutest). A surviving mutant is either untested behaviour or an assertion too weak to notice.

Run tests with a clean cache: `make test` does this.

## Security boundary

Changes to secret handling, the navigation and upload policy, or how page text reaches the agent need a test that shows the boundary holding. Do not add a switch that relaxes one. See [SECURITY.md](SECURITY.md).

## Commit messages

Changelog style, one line then a short body:

```
scope: imperative summary under 72 characters

What changed and the consequences a reader needs. Bullet the surface that
moved (flags, environment variables, commands, tools). State caveats plainly.
```

Describe what Riffle does now, not how the work went. The repository is public, so the message is too.

## Dependencies

`go.mod` names published versions only: no `replace` to a local checkout, and no require on a commit that is not on the dependency's origin. If your change needs an unreleased change in a dependency, it waits until that change is released.

## Releases

A release is a `v*` tag on a green `main`: `git tag v1.2.3 && git push origin v1.2.3`. The tag is the only version input. A tag with a suffix (`v1.2.3-rc.1`) is a prerelease: it is published under the npm `next` tag and is not sent to the MCP registry. Never move or reuse a tag; fix the cause and release the next patch version.
