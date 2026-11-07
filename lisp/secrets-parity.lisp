;;;; The Lisp side of the secret scanner's and role-label escaping's parity check
;;;; (memory/contracts/lispsecrets_test.go):
;;;;   sbcl --script lisp/secrets-parity.lisp <inputs.jsonl>
;;;; Each input line is one JSON string; for each it prints three lines: the redacted text, the
;;;; role-escaped text, each as a JSON string, and the input line redacted as JSON text
;;;; (redact-json-text), as Go's secrets.Redact, serialize.EscapeRoleLabels and
;;;; secrets.RedactJSON print them.

(load (merge-pathnames "load.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

(in-package "OM")

(let ((path (car (last sb-ext:*posix-argv*))))
  (dolist (line (split (read-file-string path) (string #\Newline)))
    (when (plusp (length line))
      (let ((text (json-parse (coerce line 'simple-string))))
        (format t "R ~A~%E ~A~%J ~A~%" (json-string (redact-secrets text)) (json-string (escape-role-labels text))
                (redact-json-text line))))))
