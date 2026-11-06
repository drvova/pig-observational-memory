;;;; Phase 2's conversation encoding at level 0 (lisp/src/encoding.lisp): the cases in
;;;; contracts/encoding.jsonl are Go's records (memory/extract encoding_test.go), which the port
;;;; must write byte for byte; the keys and the level-1 key check as Go's tests read them.

(in-package "OM")

(defparameter *encoding-tests-here* (make-pathname :name nil :type nil :defaults *load-truename*))

(defun encoding-cases ()
  (let ((path (merge-pathnames "../../contracts/encoding.jsonl" *encoding-tests-here*)))
    (loop for line in (split (read-file-string path) (string #\Newline))
          when (plusp (length line)) collect (json-parse (coerce line 'simple-string)))))

(deftest encoding-contract-cases
  (let ((cases (encoding-cases)))
    (check (> (length cases) 40) t "cases read")
    (dolist (c cases)
      (let ((entries (map 'vector #'entry-from-map (jget c "entries"))))
        (multiple-value-bind (batches) (extract entries 0 (length entries) (fold-ledger (vector)) :cwd (jget c "cwd"))
          (check (loop for b in batches append (mapcar #'obs-json (batch-observations b)))
                 (coerce (jget c "records") 'list)
                 (jget c "name")))))))

(deftest record-keys
  (dolist (c '(("Don't touch `migrations/`." ("migrations/"))
               ("Never run `pytest` directly; use `make ci-local`." ("pytest" "make ci-local"))
               ("Stay out of deploy/ and ./scripts/release.sh, and/or the docs." ("deploy/" "./scripts/release.sh"))
               ("Leave db/migrations alone (and src/gen/)." ("db/migrations" "src/gen/"))
               ("Use ` ` and `a`, then `a` again." ("a"))
               ("A 1/2 cup, w/ or a/b." ())
               ("No keys here." ())))
    (check (record-keys (first c)) (second c) (first c)))
  (check (missing-keys "Don't touch `migrations/` or `db/`." "Keep out of migrations/ entirely.") '("db/")))

(deftest level1-keeps-rule-keys
  (let ((fence (make-obs :id "c10000000001" :content "Don't touch `migrations/` while you work on this." :kind "preference" :origin "code"))
        (fact (make-obs :id "c10000000003" :content "The handler lives in `internal/api/`." :kind "fact" :origin "code")))
    (check (drops-record-p fence "Leave the migrations alone.") t)
    (check (drops-record-p fence "Leave `migrations/` alone.") nil)
    (check (drops-record-p fact "The handler lives in the api package.") nil)))
