;;;; One package for the whole spike: the Go packages it ports (transcript, extract, ledger,
;;;; selection, tables, serialize, tokens, ids) share it, prefixed where names would clash.

(defpackage "OM"
  (:use "COMMON-LISP")
  (:export "MAIN" "RUN-PIPELINE"))

(in-package "OM")

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
