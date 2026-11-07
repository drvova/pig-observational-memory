;;;; Truth maintenance against its contract cases (contracts/truth.md): contracts/truth.jsonl is
;;;; memory/ledger's output (truth_contract_test.go), which the fold, the labels and the
;;;; supersession decision here must equal.

(in-package "OM")

(defun truth-strings (v) (if (vectorp v) (coerce v 'list) '()))

(defun truth-alist (table)
  "A hash table (or a decoded object) as an alist sorted by key, or nil."
  (let ((out '()))
    (when (hash-table-p table)
      (maphash (lambda (k v) (push (cons k v) out)) table))
    (sort out #'string< :key #'car)))

(deftest truth-contract-cases
  (let ((cases (contract-cases "truth.jsonl")) (folds 0))
    (check (> (length cases) 6) t "cases read")
    (dolist (c cases)
      (let ((name (jget c "name")))
        (when (jget c "pairs")
          (check (map 'list (lambda (p) (supersession (observation-from (aref p 0)) (observation-from (aref p 1))))
                      (jget c "pairs"))
                 (truth-strings (jget c "decisions"))
                 (cat name ": decisions")))
        (when (jget c "entries")
          (incf folds)
          (let* ((entries (map 'vector #'entry-from-map (jget c "entries")))
                 (f (fold-ledger entries)))
            (check (mapcar #'obs-id (folded-observations f)) (truth-strings (jget c "observations")) (cat name ": observations"))
            (check (mapcar #'obs-id (folded-active f)) (truth-strings (jget c "active")) (cat name ": active"))
            (check (mapcar #'refl-id (folded-reflections f)) (truth-strings (jget c "reflections")) (cat name ": reflections"))
            (check (truth-alist (folded-labels f)) (truth-alist (jget c "labels")) (cat name ": labels"))
            (check (truth-alist (truth-out (folded-truth f))) (truth-alist (jget c "out")) (cat name ": out"))
            (check (truth-alist (truth-forgotten (folded-truth f))) (truth-alist (jget c "forgotten")) (cat name ": forgotten"))
            (check (mapcar #'obs-id (projection-observations (full-projection* entries)))
                   (truth-strings (jget c "projection")) (cat name ": projection"))))))
    (check (> folds 6) t "fold cases")))
