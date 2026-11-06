;;;; Loads the om-engine system with the ASDF bundled with SBCL: sbcl --load lisp/load.lisp
;;;; Compiled files go under ASDF's user cache (~/.cache/common-lisp), never into the tree.

(require :asdf)
(let ((here (make-pathname :name nil :type nil :defaults (or *load-truename* *default-pathname-defaults*))))
  (asdf:load-asd (merge-pathnames "om-engine.asd" here))
  ;; Style warnings and compiler notes are not errors; a full warning still shows.
  (handler-bind ((style-warning #'muffle-warning)
                 (sb-ext:compiler-note #'muffle-warning))
    (let ((*compile-verbose* nil) (*compile-print* nil))
      (asdf:load-system "om-engine"))))
