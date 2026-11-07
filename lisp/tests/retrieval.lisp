;;;; The search behind the reads against its contract cases (docs/protocol.md, "Retrieval"):
;;;; contracts/tokenize.jsonl and contracts/retrieval.jsonl are memory/retrieve's output, which
;;;; src/retrieve.lisp must equal byte for byte.

(in-package "OM")

(defun case-strings (v) (if (vectorp v) (coerce v 'list) '()))

(deftest tokenize-contract-cases
  (let ((cases (contract-cases "tokenize.jsonl")))
    (check (> (length cases) 20) t "cases read")
    (dolist (c cases)
      (let ((input (jget c "input")) (name (jget c "name")))
        (check (raw-tokens input) (case-strings (jget c "raw")) (cat name ": raw"))
        (multiple-value-bind (terms exact) (text-tokens input)
          (check terms (case-strings (jget c "terms")) (cat name ": terms"))
          (check exact (case-strings (jget c "exact")) (cat name ": exact")))
        (check (error-lines input nil) (case-strings (jget c "errorLines")) (cat name ": error lines"))
        (check (lookups input) (case-strings (jget c "lookups")) (cat name ": lookups"))))))

(deftest retrieval-contract-cases
  (let ((cases (contract-cases "retrieval.jsonl")) (ranked 0))
    (check (> (length cases) 12) t "cases read")
    (dolist (c cases)
      (let ((name (jget c "name")))
        (loop for triple across (or (jget c "log2") #())
              do (check (log2-fixed (round (aref triple 0)) (round (aref triple 1))) (round (aref triple 2))
                        (format nil "~A: log2 ~A/~A" name (aref triple 0) (aref triple 1))))
        (when (jget c "records")
          (incf ranked)
          (let* ((records (map 'list #'observation-from (jget c "records")))
                 (tags (tags-of records))
                 (pool (map 'vector (lambda (o) (doc-of o (obs-content o) (gethash (obs-id o) tags))) records)))
            (check (notany #'null records) t (cat name ": records read"))
            (multiple-value-bind (results partial) (retrieval-search pool (case-strings (jget c "search")))
              (check partial (jtrue-p (jget c "partial")) (cat name ": partial"))
              (check (mapcar (lambda (r)
                               (destructuring-bind (hit exact with) r
                                 (list (rdoc-id (aref pool hit)) (rdoc-tag (aref pool hit)) (and exact t)
                                       (mapcar (lambda (i) (rdoc-id (aref pool i))) with))))
                             results)
                     (map 'list (lambda (r) (list (jget r "id") (jget r "tag") (jtrue-p (jget r "exact"))
                                                  (case-strings (jget r "with"))))
                          (jget c "results"))
                     (cat name ": results")))))))
    (check (> ranked 12) t "ranking cases")))
