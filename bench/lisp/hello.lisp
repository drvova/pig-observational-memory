;;;; hello.lisp: the hello-world executable of docs/lisp-measurements.md.
(defpackage :hello (:use :cl) (:export #:main))
(in-package :hello)
(defun main () (write-line "hello") (finish-output))
