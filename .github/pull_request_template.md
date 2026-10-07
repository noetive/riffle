## What changes

<!-- The behaviour that is different afterwards, and why it had to be. -->

## Verified

<!-- CONTRIBUTING.md explains what each of these is for. Tick what you ran; CI
     runs the first one regardless. Delete the ones that do not apply. -->

- [ ] `make build test lint`
- [ ] `go test -count=1 -tags integration ./...` (needs Chrome): touches the browser
- [ ] `cd installer && npm test`: touches the installer
- [ ] Mutation tested with `mutest`: adds behaviour

## Security boundary

<!-- Does this change how secrets are handled, what a page may navigate to or
     upload, or how page text reaches the agent? Say which, or "none". -->
