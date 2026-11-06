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
                             (:file "store/neutral")))))
