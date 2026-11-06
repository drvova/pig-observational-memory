;;;; The Lisp side of the security-weakening pattern list's parity check
;;;; (memory/contracts/lispinsecure_test.go):
;;;;   sbcl --script lisp/insecure-parity.lisp <inputs.jsonl>
;;;; Each input line is one JSON string; for each it prints the rule insecure-match names, "" for
;;;; none, as Go's insecure.Match does.

(load (merge-pathnames "load.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

(in-package "OM")

(let ((path (car (last sb-ext:*posix-argv*))))
  (dolist (line (split (read-file-string path) (string #\Newline)))
    (when (plusp (length line))
      (format t "~A~%" (or (insecure-match (json-parse (coerce line 'simple-string))) "")))))
