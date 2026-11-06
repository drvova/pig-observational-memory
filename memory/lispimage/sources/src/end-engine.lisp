;;;; Exports what OM.ENGINE owns, once its files have loaded (src/package.lisp), and the entry
;;;; points parity.lisp calls as OM:MAIN and OM:RUN-PIPELINE.

(in-package "OM.ENGINE")

(export-own-symbols)

(export '(main run-pipeline) "OM")
