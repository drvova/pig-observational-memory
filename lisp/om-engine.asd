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
                             (:file "end-base")
                             (:file "entry")
                             (:file "ledger")
                             (:file "ledger-subjects")
                             (:file "ledger-reflections")
                             (:file "ledger-replace")
                             (:file "ledger-fold")
                             (:file "ledger-render")
                             (:file "end-kernel")
                             (:file "transcript")
                             (:file "end-readers")
                             (:file "extract")
                             (:file "selection")
                             (:file "pipeline")
                             (:file "pipeline-ledger")
                             (:file "end-engine")
                             ;; W2: the transcript readers and the neutral store.
                             (:file "readers/common")
                             (:file "readers/formats")
                             (:file "readers/sqlite")
                             (:file "readers/paths")
                             (:file "store/neutral")))
               ;; om.server: the image's socket, sessions and events (docs/lisp-port-image-client.md).
               (:module "server"
                :pathname "src/server/"
                :serial t
                :components ((:file "package")
                             (:file "jsonrpc")
                             (:file "locks")
                             (:file "sessions")
                             (:file "engine")
                             (:file "server")
                             (:file "methods")))
               ;; W4, tool and layer (docs/lisp-port-tool-layer.md)
               (:module "w4"
                :pathname "src"
                :serial t
                :components ((:file "tool/package")
                             (:file "layer/limits")
                             (:file "tool/files")
                             (:file "tool/curated")
                             (:file "layer/reader")
                             (:file "layer/runner")
                             (:file "layer/settle")
                             (:file "layer/store")
                             (:file "layer/adaptable")
                             (:file "tool/reads")
                             (:file "layer/lock")))
               ;; W5, hooks (docs/lisp-port-hooks.md)
               (:module "w5"
                :pathname "src"
                :serial t
                :components ((:file "hooks/serialize")
                             (:file "hooks/compaction")
                             (:file "hooks/thin")
                             (:file "hooks/view")
                             (:file "server/hooks")))))
