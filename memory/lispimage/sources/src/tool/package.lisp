;;;; W4's packages (docs/lisp-port-tool-layer.md): OM.TOOL is the fixed API model code calls,
;;;; OM.LAYER the machinery behind it (the safe reader, the runner, the layer store, adaptable
;;;; dispatch, the evidence rule and the curated store). Both read the spike's internals through
;;;; om:: so a later move of the OM package (W1) only changes those prefixes.
;;;;
;;;; OM.ENGINE (the defaults of the adaptable functions) is made here only when no other file made
;;;; it first, so W1's package map can own its definition.

(eval-when (:compile-toplevel :load-toplevel :execute)
  (require :sb-posix))

(defpackage "OM.TOOL"
  (:use)
  (:documentation "The API model code calls from the memory tool and from layer definitions.")
  (:export
   ;; reads
   "NOTES" "HISTORY" "RECALL" "SAVED" "NOTE-BY-ID" "HEAD"
   ;; a note (one record) and a history row
   "NOTE-ID" "NOTE-WHEN" "NOTE-KIND" "NOTE-SUBJECT" "NOTE-SCOPE" "NOTE-RELEVANCE" "NOTE-CONTENT"
   "NOTE-STATUS" "NOTE-EVENT-DATE" "NOTE-ORIGIN" "NOTE-OUTCOME" "NOTE-TOOL" "NOTE-TARGET"
   "NOTE-SOURCE" "NOTE-ANCHOR" "NOTE-LINE" "NOTE-P" "NOTE-SUPERSEDED-BY" "NOTE-TAG" "NOTE-WITH"
   "NOTE-FRESH"
   ;; the HEAD structure view (om.HeadView)
   "HEAD-VIEW-REPO-ID" "HEAD-VIEW-HEAD" "HEAD-VIEW-PREFIX" "HEAD-VIEW-FILES" "HEAD-VIEW-OFFSET"
   "HEAD-VIEW-TOTAL" "HEAD-VIEW-TRUNCATED" "HEAD-VIEW-NEXT" "HEAD-VIEW-P"
   "TURN-ID" "TURN-WHEN" "TURN-ROLE" "TURN-TEXT" "TURN-P"
   "MEMORY-NAME" "MEMORY-TYPE" "MEMORY-DESCRIPTION" "MEMORY-BODY" "MEMORY-P"
   ;; curated writes
   "SAVE" "EDIT" "FORGET-MEMORY" "FEEDBACK"
   ;; the model's layer
   "DEFINE" "LAYER" "LAYER-FORGET" "LAYER-ROLLBACK" "ADAPTABLES"
   ;; output
   "SHOW"
   ;; the call's strings argument (the memory tool in a child)
   "TEXT"))

(defpackage "OM.LAYER"
  (:use "COMMON-LISP")
  (:documentation "The safe reader, the runner, the layer, adaptable dispatch and the curated store.")
  (:export
   ;; the safe reader and runner
   "SAFE-READ-ALL" "REFUSED" "REFUSED-REASON" "RUN-TOOL-CODE" "RUN-SANDBOXED" "TOOL-RESULT"
   "*TIMEOUT-SECONDS*" "*HEAP-LIMIT-BYTES*"
   ;; the call context
   "MAKE-CALL" "*CALL*" "WITH-CALL"
   ;; adaptable functions
   "DEFINE-ADAPTABLE" "CALL-ADAPTABLE" "*SESSION-MODEL*"
   ;; the layer
   "*LAYER-ROOT*" "LAYER-HOLD" "LAYER-SETTLED" "LAYER-HELD" "LAYER-FORGET" "LAYER-ROLLBACK"
   "LAYER-SETTLE" "LAYER-RAN-HELD" "LAYER-FAILURES" "SETTLE-V2" "READ-MODEL-EVENTS"
   ;; the curated store
   "CURATED-RUN" "CURATED-CHECK" "CURATED-DESCRIBE"
   ;; cooperative limits and the image's health (layer/limits.lisp)
   "*FUEL-STEPS*" "*STOP-GRACE-SECONDS*" "IMAGE-NEEDS-RESTART"))

(unless (find-package "OM.ENGINE")
  (make-package "OM.ENGINE" :use '("COMMON-LISP")))
