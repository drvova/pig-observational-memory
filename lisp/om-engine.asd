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
                             (:file "entry")
                             (:file "transcript")
                             (:file "ledger")
                             (:file "extract")
                             (:file "selection")
                             (:file "pipeline")))
               ;; W4, tool and layer (docs/lisp-port-tool-layer.md)
               (:module "w4"
                :pathname "src"
                :serial t
                :components ((:file "tool/package")
                             (:file "tool/files")
                             (:file "tool/curated")
                             (:file "layer/reader")
                             (:file "layer/runner")
                             (:file "layer/settle")
                             (:file "layer/store")
                             (:file "layer/adaptable")
                             (:file "tool/reads")
                             (:file "layer/lock")))))
