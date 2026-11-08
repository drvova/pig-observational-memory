;;;; om-engine: a spike port of the memory engine's read-and-record path (transcript reading,
;;;; level-0 extraction, the ledger fold and the tables summary) to SBCL Common Lisp. It needs
;;;; nothing beyond SBCL and its bundled contribs (docs/lisp-spike.md).

(defsystem "om-engine"
  :description "Spike port of the memory engine's read-and-record path"
  :serial t
  :components ((:module "src"
                :serial t
                :components ((:file "package")
                             (:file "util")
                             (:file "json")
                             (:file "regex")
                             (:file "sha256")
                             (:file "timefmt")
                             ;; The secret scanner and role-label escaping (contracts/secrets.md).
                             (:file "secrets")
                             (:file "roles")
                             ;; Preferences that weaken security (contracts/insecure.md).
                             (:file "insecure")
                             (:file "end-base")
                             (:file "entry")
                             (:file "ledger")
                             ;; Phase 2: the encoding fields (contracts/encoding.md).
                             (:file "ledger-encoding")
                             (:file "ledger-subjects")
                             (:file "ledger-reflections")
                             (:file "ledger-replace")
                             ;; Phase 5: truth maintenance (forget tombstones, the cascade).
                             (:file "ledger-truth")
                             (:file "ledger-fold")
                             (:file "ledger-render")
                             ;; The records a preference that weakens security holds.
                             (:file "ledger-held")
                             (:file "end-kernel")
                             (:file "transcript")
                             (:file "end-readers")
                             (:file "extract")
                             ;; Phase 4: per-repository project records and freshness verdicts.
                             (:file "project")
                             ;; Level 0's reading of tool output (memory/extract/output.go).
                             (:file "output")
                             ;; Phase 2: rules, scope exclusions, decisions and repo conventions at level 0.
                             (:file "conversation")
                             ;; Phase 2: episodes and assistant pushback beside user claims.
                             (:file "contest")
                             ;; Phase 3: working memory (receipts, staleness, rule replacements).
                             (:file "working")
                             ;; Phase 8: cues after a tool call (memory/extract/cue.go).
                             (:file "cue")
                             ;; Phase 5: the labels and the three-way supersession decision.
                             (:file "labels")
                             ;; Phase 4: the search behind the reads (memory/retrieve).
                             (:file "retrieve")
                             (:file "selection")
                             (:file "pipeline")
                             (:file "pipeline-ledger")
                             ;; The promise syndrome (memory/ledger/promise.go).
                             (:file "promise")
                             ;; Phase 7: the sleep pass (memory/sleep).
                             (:file "sleep")
                             (:file "end-engine")
                             ;; W2: the transcript readers and the neutral store.
                             (:file "readers/common")
                             (:file "readers/formats")
                             (:file "readers/sqlite")
                             (:file "readers/paths")
                             (:file "store/neutral")
                             ;; Durable appends to the append-only files (docs/lisp-port-tool-layer.md).
                             (:file "store/append")))
               ;; om.server: the image's socket, sessions and events (docs/lisp-port-image-client.md).
               (:module "server"
                :pathname "src/server/"
                :serial t
                :components ((:file "package")
                             (:file "jsonrpc")
                             (:file "locks")
                             (:file "sessions")
                             (:file "engine")
                             ;; W6: the two-way peer, the connection's state, the build id.
                             (:file "peer")
                             (:file "connection")
                             (:file "build")
                             (:file "server")
                             (:file "methods")))
               ;; W4, tool and layer (docs/lisp-port-tool-layer.md)
               (:module "w4"
                :pathname "src"
                :serial t
                :components ((:file "tool/package")
                             (:file "layer/limits")
                             (:file "tool/files")
                             ;; Phase 6: the shared core log (memory/core).
                             (:file "core")
                             (:file "tool/curated")
                             (:file "layer/reader")
                             (:file "layer/runner")
                             (:file "layer/settle")
                             (:file "layer/store")
                             (:file "layer/adaptable")
                             (:file "tool/reads")
                             ;; W3t: the memory tool in a child SBCL (docs/lisp-port-tool-layer.md)
                             (:file "tool/snippets")
                             (:file "layer/child")
                             (:file "tool/memory-tool")
                             (:file "layer/lock")
                             ;; 1b, the curated ranked index (docs/lisp-port-tool-layer.md)
                             (:file "tool/curated-index")
                             ;; Curated memories that weaken security are held.
                             (:file "tool/curated-held")
                             ;; W3t: the fixed texts the image hands with the Lisp forms
                             (:file "tool/lisp-texts")))
               ;; W4 next steps, evidence and retirement (docs/lisp-port-evidence.md)
               (:module "rules"
                :pathname "src"
                :serial t
                :components ((:file "rules/rows")
                             (:file "rules/golayer")
                             (:file "rules/rules")
                             (:file "rules/detect")
                             (:file "rules/evidence")))
               ;; W5, hooks (docs/lisp-port-hooks.md)
               (:module "w5"
                :pathname "src"
                :serial t
                :components ((:file "hooks/serialize")
                             (:file "hooks/compaction")
                             (:file "hooks/thin")
                             (:file "hooks/view")
                             (:file "server/hooks")))
               ;; W6, the first PiG events over engine-rpc (docs/lisp-port-peer.md)
               (:module "w6"
                :pathname "src"
                :serial t
                :components ((:file "server/events")))
               ;; Compaction on PiG and the remaining PiG events (docs/lisp-port-peer.md)
               (:module "pig"
                :pathname "src"
                :serial t
                :components ((:file "server/compaction-harness")
                             (:file "server/pig-session")
                             (:file "server/pig-compaction")))
               ;; W3t, the memory tool on the image (docs/lisp-port-tool-layer.md)
               (:module "w3t"
                :pathname "src"
                :serial t
                :components ((:file "server/tool")))
               ;; The retired names and the v1 layer migration at a session start (docs/lisp-port-migration.md)
               (:module "migration"
                :pathname "src"
                :serial t
                :components ((:file "layer/go-layer")
                             (:file "server/migration")))
               ;; Level 1 on PiG (docs/lisp-port-level1.md)
               (:module "level1"
                :pathname "src"
                :serial t
                :components ((:file "consolidate/serialize")
                             (:file "consolidate/prompt")
                             (:file "consolidate/validate")
                             ;; Phase 8: the summary's optional dream section (memory/dreams).
                             (:file "dreams")
                             (:file "consolidate/layer")
                             (:file "server/level1")))
               ;; /om:status and the remaining harness reads (docs/lisp-port-status.md)
               (:module "status"
                :pathname "src"
                :serial t
                :components ((:file "legacy/legacyquery")
                             (:file "legacy/notes")
                             (:file "harness/rule-migration")
                             (:file "harness/profile")
                             (:file "harness/floor")
                             (:file "harness/retired")
                             (:file "harness/layer-status")
                             (:file "server/status")))
               ;; Phase 7: the sleep pass at the image's idle, in a child SBCL (decision 9).
               (:module "sleep"
                :pathname "src"
                :serial t
                :components ((:file "server/sleep")))))
