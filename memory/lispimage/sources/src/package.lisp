;;;; The packages of the Lisp core (docs/lisp-core.md, "Packages"), as far as the port has come:
;;;;   OM.BASE     JSON, regex, SHA-256, time formatting, utilities (stand-ins for Go's library)
;;;;   OM.KERNEL   entries, records, the ledger fold, subjects, the status rule (locked)
;;;;   OM.READERS  transcript readers
;;;;   OM.ENGINE   the default definitions: extraction, selection, the pipeline
;;;;   OM          everything above, for tests, tools and work not yet placed in a package
;;;; Each package :use's the ones below it. Until the export lists are narrowed, every symbol a
;;;; package owns is exported once its group has loaded (src/end-*.lisp), so code read in OM, or
;;;; in a package that :use's OM, sees every name it saw when the port was one package.

(defpackage "OM.BASE"
  (:use "COMMON-LISP")
  (:export "EXPORT-OWN-SYMBOLS"))

(defpackage "OM.KERNEL" (:use "COMMON-LISP" "OM.BASE"))
(defpackage "OM.READERS" (:use "COMMON-LISP" "OM.BASE" "OM.KERNEL"))
(defpackage "OM.ENGINE" (:use "COMMON-LISP" "OM.BASE" "OM.KERNEL" "OM.READERS"))
(defpackage "OM" (:use "COMMON-LISP" "OM.BASE" "OM.KERNEL" "OM.READERS" "OM.ENGINE"))

(in-package "OM.BASE")

(defmacro export-own-symbols ()
  "Exports every symbol whose home is the current package."
  `(eval-when (:compile-toplevel :load-toplevel :execute)
     (let ((package *package*) (own '()))
       (do-symbols (s package) (when (eq (symbol-package s) package) (push s own)))
       (export own package))))

;;; #r<d>...<d> reads a raw string between two copies of the delimiter character d, with no
;;; escapes, so a regular expression is written as Go's backquoted literal writes it.
(eval-when (:compile-toplevel :load-toplevel :execute)
  (defun read-raw-string (stream subchar arg)
    (declare (ignore subchar arg))
    (let ((delimiter (read-char stream t nil t)))
      (with-output-to-string (out)
        (loop for c = (read-char stream t nil t)
              until (char= c delimiter)
              do (write-char c out)))))
  (set-dispatch-macro-character #\# #\r #'read-raw-string))
