# Lisp port: transcript readers and the neutral store (W2)

This is work package W2 of [lisp-core.md](lisp-core.md): every transcript reader in `memory/transcript`
and the neutral store and branch merge of `memory/neutral`, ported to SBCL in `lisp/`, with a parity
harness that reads the real-transcript corpus with both engines and compares the output byte for byte.

## What was ported

| Lisp file | Go source | What it holds |
|---|---|---|
| `lisp/src/readers/common.lisp` | `transcript.go`, `kimi.go`, `sqliterows.go`, `more.go` | `fmt.Sprint` of decoded JSON (`go-sprint`, `strconv` `'g'` floats), `unixTime`, `unixSeconds`, `unixMillis`, `joinLines`, `jsonColumn`, the assistant group Codex and Kimi build |
| `lisp/src/readers/formats.lisp` | `codex.go`, `copilot.go`, `cursor.go`, `deepagents.go`, `droid.go`, `pi.go`, `gemini.go`, `kimi.go`, `qwen.go`, `kiro.go`, `more.go`, `more2.go`, `generic.go` (`Formats`) | the 16 readers the spike lacked, and the registry of all 18 by harness name (`transcript-file-entries`) |
| `lisp/src/readers/sqlite.lisp` | `sqlite.go`, `sqliterows.go` | `QuerySQLite` through `sqlite3 -readonly -json` or `python3 -I` (the same script), `SQLQuote`, the Crush, Goose and Amazon Q rows readers and `DBFormats` (`db-entries`) |
| `lisp/src/readers/paths.lisp` | the `*Path` functions | where Kode, Copilot, Kimi, Kiro, Cline, Cortex, Auggie, Crush, Goose and Amazon Q keep a session, and `AmazonQConversation` |
| `lisp/src/store/neutral.lisp` | `store.go`, `host.go` (read, appendEntry) | `Stored`, `StoreFor`, `SafeName`, `Load`, `Append` under the same `flock(2)` lock Go's `filelock` takes, `Branch`, `Aliases`, and a host that merges a transcript with its store, caching each while its file is unchanged, and appends entries after the tip |
| `lisp/readers-parity.lisp` | — | the command the Go harness runs |
| `lisp/tests/readers.lisp`, `lisp/tests/store.lisp` | `hookformats_test.go`, `transcript_test.go`, `real_test.go`, `neutral_test.go` | 16 tests, 122 checks; the samples and expectations are Go's |
| `memory/transcript/lispparity_test.go` | — | the parity harness (`OM_LISP_PARITY=1`, sbcl on PATH; skipped otherwise) |

The code stays in the spike's `OM` package, in its own files, so W1's package move can carry it as it is.
SQLite is never linked: like Go, the image runs `sqlite3` when it is on PATH, else Python's built-in module.

## Parity

```sh
sbcl --non-interactive --load lisp/run-tests.lisp                          # Lisp tests
cd memory && OM_LISP_PARITY=1 go test -p 1 ./transcript -run Lisp -v       # parity
```

| Case | What both engines do | Result |
|---|---|---|
| `TestLispReadersParity` | each of the 33 JSON and JSONL files in `memory/transcript/testdata/real` read in every one of the 18 formats (594 readings: the right format and every wrong one, which runs each reader on shapes it was not written for) | identical: 1,118 entries |
| `TestLispReadersParityRows` | `crush-db.sql` and `goose-db.sql` loaded into databases and read through `QuerySQLite` (python3 here; no `sqlite3` is installed); assembled rows: Goose's message samples plus an error result and a hidden turn context, an Amazon Q conversation with `Json` results, Crush rows with a summary row and an undecodable column | identical: 12, 14, 4, 5, 4 entries |
| `TestLispStoreParity` | stored entries placed after the first entry, an alias of a grouped entry, another stored entry, the tip, a lost id and the start, plus lines Load must skip, merged into five real transcripts (`Branch`); and the same entries appended by each engine (`Store.Append`) | branch identical: 19, 13, 41, 126, 130 entries; the two store files byte for byte equal |

Each entry is compared as `json.Marshal(entry.ToMap())`, so ids, aliases, timestamps (string and
milliseconds), block order, `null` against `[]`, float formatting and HTML escapes all count. Every case
matched on its first run. The spike's harness (`memory/selection`, `TestLispParity`) still passes.

## Not ported, and differences

- **Every format is ported.** The 18 file formats and the 3 database formats all read identically on the
  corpus. Auggie, iFlow and Amazon Q have no real fixture: Auggie and iFlow are covered by Go's
  hand-written samples in the Lisp tests and, as wrong formats, on every real file; Amazon Q by the
  assembled conversation in the parity harness.
- **The neutral compaction** (`compact.go`: `Compaction`, `CompactionAt`, `tokensBefore`, `LatestSummary`)
  and the rest of `Host` (notify, tool registration, the model stubs) need the hooks and the ledger's
  compaction helpers; they belong to W3 and W5.
- **File versions.** Go keys its caches on size and modification time in nanoseconds; SBCL's `stat` has
  whole seconds, so the Lisp version is inode, size and seconds, and a file modified within the last two
  seconds has no version and is read again. Only the in-process cache uses it.
- **Store keys.** `encoding/json` matches `after` and `entry` case-insensitively and takes the last match
  in document order; a decoded object keeps no order, so Lisp takes the exact key, else the first
  case-insensitive one in sorted order. The engine writes only the exact keys.
- **Inherited from the spike:** invalid UTF-8 becomes U+FFFD per malformed sequence (Go: per byte), and
  SBCL 2.2.9's Unicode tables are older than Go's. No fixture has either.
- **Windows.** The store lock is `flock(2)`; the `LockFileEx` lock of Go's Windows build is not ported.
