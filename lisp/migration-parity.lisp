;;;; The Lisp side of the layer migration's parity test (memory/contracts/lispmigration_test.go):
;;;;   sbcl --script lisp/migration-parity.lisp <store dir> <model> <worker> <clock> <taken tools>
;;;; runs a session start's layer part over the curated store at <store dir> with the clock fixed:
;;;; harness.Manager.NoteRetired (the host's tools are the comma-separated <taken tools>), then
;;;; harness.MigrateLayerV2 for <model> and <worker> ("-" for none). It prints each message a host
;;;; with a UI is told ("notice: ..."), then the skip list ("skip: memory,..."), as the Go side does.

(load (merge-pathnames "load.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

(in-package "OM.GOLAYER")

(destructuring-bind (store model worker clock taken) (subseq sb-ext:*posix-argv* (- (length sb-ext:*posix-argv*) 5))
  (let* ((at (om.layer::parse-rfc3339 clock))
         (om.layer::*now* (lambda () (cons (car at) 0)))
         (model (if (string= model "-") "" model))
         (worker (if (string= worker "-") "" worker))
         (taken (if (string= taken "-") '() (om::split taken ",")))
         (retired (retired-query-names store))
         (skip (sort (remove-duplicates
                      (loop for name in retired
                            when (and (string/= name "") (string/= name "memory") (not (member name taken :test #'string=)))
                              collect name)
                      :test #'string=)
                     #'string<)))
    (dolist (message (migrate-layer-v2 store model worker))
      (format t "notice: ~A~%" (om::replace-all message (string #\Newline) "\\n")))
    (format t "skip: ~{~A~^,~}~%" (cons "memory" skip))))
