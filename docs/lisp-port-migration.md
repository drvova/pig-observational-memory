# Lisp port: the retired names and the v1 layer migration

This port moves the last two parts of a session start that sent PiG's `event/sessionStart` to Go
onto the image: the retired defined tools' names (`harness.Manager.NoteRetired`, `SkipNames`) and
the v1 layer migration (`engine.MigrateLayer`, `harness.MigrateLayerV2`). The image now starts a
session over any store Go starts, except one case (see "Left"). Om's `session-start` no longer
leaves `migrate-layer` to the Go client.

## Files

| File | Go source | What it holds |
|---|---|---|
| `lisp/src/layer/go-layer.lisp` (package `OM.GOLAYER`) | `memory/layer` (`checkStored`, `stale`, `load`, `List`, `recordedFile`, `stampLifted`, `putStamped`, `write`, `recordHeld`, `MigrateInstructions`, `MigrateToV2`, `MigrateWorkerToV2`, the messages), `memory/kernel` (`Owns`, `Check`), `tables/rows_decode.go`, `curated/definitions.go` (`migrateDefinitions`, `ListDefinitions`, `RefusedDefinitions`, `parseDefinition`), `legacyquery.CheckDefinitionBounds`, `harness.RetiredQueries`, `harness.MigrateLayerV2`, `agents.ConsolidationInstructions` | The Go layer's instructions and tool definitions, with the kernel's check on every load; the legacy definitions' move into the layer; the instructions migrations. It builds on the evidence port's reader (`lisp/src/rules/golayer.lisp`): the header codec, the manifest's states, the held index and the put generation, whose line writer now also takes `session`, `reason`, `from` and `held`. |
| `lisp/src/server/migration.lisp` | `harness.Manager.NoteRetired`, `SkipNames`, `MemoryToolNames`, `DefinedQueries`, `WorkerModelKey`, `engine.MigrateLayer` | The session start's layer part on PiG and on om. |
| `lisp/src/server/connection.lisp` (edited) | `config.normalizeSettings`, `removedValueValid`, `normalizeFloor` | The removed settings (`RemovedSet`, in `config.RemovedSettings` order), the removed `model` setting as provider and id, and whether `kernelFloor` asks for a lowering with who and why. The connection keeps the retired names. |
| `lisp/migration-parity.lisp` | | The Lisp side of the parity test. |
| `lisp/tests/migration.lisp` | | Six tests: the strict rows decoder, the kernel's check, the fold blocks, MigrateLayerV2 over a store, the legacy definitions, and a PiG session start whose skip list a turn end reads. |

## What the image does now

- **`event/sessionStart`** (PiG): the pin reset, then with a store `NoteRetired`, `MigrateLayer`
  and the curated settle, in Go's order.
  - `NoteRetired` reads `RetiredQueries`' names: the tool definitions kept for all models that
    load and parse, then the legacy files the layer refused. Reading them moves
    `definitions/*.rows` into the layer first, as `curated.Store.ListDefinitions` does: a
    `harness/_2a/tool/<name>.rows` file, its blob and its put generation, and the legacy file
    renamed `.migrated`, or `.refused` when the kernel or the store's checks refuse it.
  - Each name joins the connection's set, except the memory tool's and a name a tool of
    `host/allTools` holds. A failed `host/allTools` adds nothing.
  - `MigrateLayer` takes the session model's key and the worker key from the removed `model`
    setting (`WorkerModelKey`: the setting's provider, else the session model's; `models/find`'s
    model when the host has one; nothing when it is the session model). It then runs
    `MigrateInstructions` for every model and the session model, `MigrateToV2` for both, and
    `MigrateWorkerToV2`. Each migration is told through `host/notify` when the host has a UI. A
    step that fails is logged and dropped whole, and is tried again at the next start.
- **The skip list** (`conn-skip-tools`): the memory tool, then the connection's retired names in
  name order. Level 0 at a turn end, the extraction and minimal brief inside a compaction, the
  recovered compaction and the thinning plan all read it. The detectors get `DefinedQueries`'
  names (the listed definitions the skip list holds), and a call of one signals
  `legacy-memory-call`, so that turn end is refused before any write.
- **Om's `session-start`**: the same over the root's store, with no session model, no tool
  registry and no UI. The names are read for their side effect, then `MigrateLayerV2` runs for
  every model. Nothing is returned as `remaining`.
- **Lifted refusals**: the session start's refusals for retired tools, a v1 layer and the
  removed `model` setting; the connection mark that refused later `turnEnd`, `context` and
  `sessionBeforeCompact` events; and the turn end's refusal for retired defined tools in the store.

## The skip list's lifetime (decision)

Go keeps one `harness.Manager` per engine process, and its retired set only grows. A PiG process
keeps one engine-rpc connection to the image across its sessions, so the image keeps the set on
the connection. It grows at each session start and is dropped with the connection, as Go's is
dropped with the process. Keeping it per image would share names between PiG processes, which Go
never does. On om, each hook is its own Go process: the names a session start reads die with it,
and later events use the memory tool alone. So the image keeps no list for om, and om's events
keep `memory` alone.

## Parity

```sh
sbcl --non-interactive --load lisp/run-tests.lisp
cd memory && OM_LISP_PARITY=1 go test -count=1 -p 1 ./contracts ./enginerpc -run 'LispLayerMigration|LispConsolidationSeed|LispSessionStartMigratesLayer'
```

- **`TestLispLayerMigrationMatchesGo`** (`memory/contracts/lispmigration_test.go`) builds six
  stores with the Go layer and copies each one. It then runs Go's `NoteRetired` and
  `MigrateLayerV2` on one copy and `lisp/migration-parity.lisp` on the other, with the clock
  fixed. It compares the messages a UI is told, the skip list, and every file under the store
  byte for byte: layer files, blobs, held index, manifest lines, and the legacy definitions'
  renames. It then starts a second time and compares again, which must change nothing. The
  layer files the port wrote validate against `contracts/schemas` (`layer-generation`,
  `layer-held-index`).
  - The cases are:
    - the v1 session of `hooks/migration_v2_test.go`, with defined queries, one taken by a host
      tool, one too long to list, and legacy files: migrated, kernel-owned, and a name the layer
      holds;
    - inheritance from the all-models layer, with JSON-escaped text;
    - a worker model with extend, replace and dropper texts;
    - files recorded before modes: a written consolidation that repeats the seed, an
      all-models observer, a held reflector with its fallback, and an untracked file;
    - a held consolidation, which makes the folds wait;
    - om's case, with no session model.
  - All six match.
- **`TestLispConsolidationSeed`** (not gated) checks the port's copy of the consolidation seed
  against `agents.ConsolidationInstructions`, since a written consolidation definition records
  that seed's digest.
- **`TestLispSessionStartMigratesLayer`** (`memory/enginerpc`) runs PiG's sequence on the image
  over the socket and on the in-process path: session start, turn end, session start. The store
  is one v1 left, with the removed `model` setting naming a worker. The image runs every event
  itself, with no fallback logged. The migration notices, the appended entries (the retired
  `notes` tool's call is left out of the records, `bash`'s is kept) and the store's files equal
  Go's, apart from times and digests.
- The whole gated suite (`./selection ./transcript ./contracts ./cli ./enginerpc -run Lisp`)
  passes.

## Left

- **A lowered kernel floor** (`kernelFloor` with `lower`, `requestedBy` and `reason`). It admits
  definitions the kernel refuses, and `harness.floorOf` records it in `harness/floor.jsonl`. The
  image checks with the floor engaged and records nothing. So a PiG session start under a lowered
  floor with layer data to read is refused (not ported) before any write. On om, the image
  leaves `migrate-layer` to the Go client and moves no legacy definition.
- **Other kinds.** Only instructions and tools are loaded. The detector, remedy, format, schema
  and summary validators (`rules`, `declare`) are not needed at a session start. Loading one of
  those kinds signals an error instead of skipping its check.
- **`om.Retired()` and `/om:status`** list each retired query with its snippet
  (`legacyquery.Hint`) and its reason. The image reads only the names. Both are the memory
  tool's and status's work, and neither is ported.
- **Removed settings** are parsed (`rcfg-removed`), ignored, and listed only by `/om:status`,
  which still falls back to Go.
- **Header decoding** uses the evidence port's reader, which differs from Go's decoder on some
  hand-edited headers (extra spacing, scalars). Such a file must also match the digest its
  manifest records, so only a header Go wrote is read. Definition texts, which `kernel.Check`
  decides on, use the strict decoder.
