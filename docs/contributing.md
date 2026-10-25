# Contributing

This page says where changes go, what a pull request should contain, and which checks it must pass.

## Where changes land

The extension is the Go module in `extensions/observational-memory`, run as a [PiG](https://github.com/MichaelKinsy/PiG) extension. [pig.md](pig.md) shows its package layout. A change that makes it behave differently from the TypeScript extension adds an entry to the migration notes there, so users moving over know what to expect; a design change updates [memory-v2.md](memory-v2.md).

## What a pull request contains

* **One concern.** A bug fix, a feature or a docs change, not several. Keep unrelated cleanups out.
* **A test that fails without the change.** Put it next to the code it covers, using the fakes in `internal/testfix` instead of a live model or host. A fix for a race needs a test that runs under the race detector.
* **Docs that match.** A change to settings, `/om:status` output, the `memory` tool or compaction behaviour updates [configuration.md](configuration.md), [how-it-works.md](how-it-works.md) or the README in the same pull request.
* **A description of the behaviour change.** Say what a user or the agent sees before and after, and why. Link the issue it closes.

Two areas need extra care, because a mistake there loses memory silently:

* **Token accounting.** The compaction trigger, the summary budget and memory storage count tokens in separate domains. A change to one must keep the trigger, `/om:status`, the docs and the tests aligned.
* **Coverage markers.** A coverage marker claims that every source entry up to it was read. Any code that appends one must have read every source entry after the previous marker. Compaction must never discard source entries no extraction read.
* **Requests.** The extension makes no model request outside a compaction and at most one inside it. Every request goes through `agents.Request`; tests count them with `testfix.CountRequests`.

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
