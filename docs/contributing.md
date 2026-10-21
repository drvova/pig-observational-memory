# Contributing

This page says where changes go, what a pull request should contain, and which checks it must pass.

## Where changes land

The Go port in `extensions/observational-memory` is the maintained implementation. It runs as a [PiG](https://github.com/MichaelKinsy/PiG) extension. Fix bugs and add features there.

The TypeScript extension in `src/` (built to `dist/index.ts`) is frozen pending its removal. Do not add features to it. Where the Go port behaves differently, the difference is listed in [pig.md](pig.md), and a change that adds one updates that list.

## What a pull request contains

* **One concern.** A bug fix, a feature or a docs change, not several. Keep unrelated cleanups out.
* **A test that fails without the change.** Put it next to the code it covers, using the fakes in `internal/testfix` instead of a live model or host. A fix for a race needs a test that runs under the race detector.
* **Docs that match.** A change to settings, `/om:status` output, the `recall` tool or compaction behaviour updates [configuration.md](configuration.md), [how-it-works.md](how-it-works.md) or the README in the same pull request.
* **A description of the behaviour change.** Say what a user or the agent sees before and after, and why. Link the issue it closes.

Two areas need extra care, because a mistake there loses memory silently:

* **Token accounting.** Compaction, consolidation and memory storage count tokens in separate domains. A change to one must keep the trigger, `/om:status`, the docs and the tests aligned.
* **Coverage markers.** A coverage marker claims that every source entry up to it was read. Any code that appends one must have read every source entry after the previous marker. Compaction must never discard source entries the observer has not covered.

`AGENTS.md` holds the full list of these invariants.

## Checks

Run these from `extensions/observational-memory` before opening a pull request. CI runs the same three.

```bash
gofmt -l .
go vet ./...
go test -race ./...
```

The first command must print nothing. A change to the extension's registrations should also pass PiG's validator:

```bash
pig install ./extensions/observational-memory --validate-only --json
```

The extension's `Name` must stay equal to its directory name, `observational-memory`. PiG identifies a factory by that directory, and a test checks the two match.
