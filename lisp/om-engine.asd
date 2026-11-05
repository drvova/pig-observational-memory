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
                             (:file "methods")))))
