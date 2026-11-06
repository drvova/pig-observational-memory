;;;; The secret scanner and role-label escaping against their contract cases (contracts/secrets.md):
;;;; contracts/secrets.jsonl and contracts/roles.jsonl are Go's output, which the port must equal
;;;; byte for byte.

(in-package "OM")

(defparameter *secrets-tests-here* (make-pathname :name nil :type nil :defaults *load-truename*))

(defun contract-cases (file)
  (let ((path (merge-pathnames (concatenate 'string "../../contracts/" file) *secrets-tests-here*)))
    (loop for line in (split (read-file-string path) (string #\Newline))
          when (plusp (length line)) collect (json-parse (coerce line 'simple-string)))))

(defun case-input (c)
  (let ((input (jget c "input")))
    (if (stringp input) input (apply #'concatenate 'string (coerce input 'list)))))

(deftest secrets-contract-cases
  (let ((cases (contract-cases "secrets.jsonl")))
    (check (> (length cases) 30) t "cases read")
    (dolist (c cases)
      (let ((got (redact-secrets (case-input c))))
        (check got (jget c "output") (jget c "name"))
        (check (redact-secrets got) got (cat "idempotent: " (jget c "name")))))))

(deftest roles-contract-cases
  (let ((cases (contract-cases "roles.jsonl")))
    (check (> (length cases) 8) t "cases read")
    (dolist (c cases)
      (check (escape-role-labels (case-input c)) (jget c "output") (jget c "name")))))

(deftest secrets-entropy-exact
  (check (entropy-at-least-p "aabbccddefghijkl" 7 2) t "3.5 bits is at least 3.5")
  (check (entropy-at-least-p "aabbccddefghijkl" 36 10) nil "3.5 bits is below 3.6")
  (check (entropy-at-least-p "abcdefghijklmnop" 4 1) t "16 distinct characters")
  (check (entropy-at-least-p "aaaa" 1 10) nil "no entropy"))

(deftest secrets-redact-json
  (check (redact-json-text "{\"a\":\"x\",\"b\":[\"password=hunter22x\"],\"c\":{\"password\":\"hunter22x\"}}")
         "{\"a\":\"x\",\"b\":[\"password=[redacted:password-assignment]\"],\"c\":{\"password\":\"hunter22x\"}}")
  (check (redact-json-text "{\"content\":\"nothing <here>\"}") "{\"content\":\"nothing <here>\"}" "untouched")
  (check (redact-json-text "{\"content\":\"password=hunter22x <b>\"}")
         "{\"content\":\"password=[redacted:password-assignment] \\u003cb\\u003e\"}" "re-encoded as Go does"))
