;;;; The summary's optional dream section (memory/dreams; docs/brain-memory.md, decision 5;
;;;; docs/memory-v2.md, "Dreams"), behind the dreams setting, which ships off: code-only links,
;;;; counterfactual skeletons, hindsight relabels and rehearsal of open intentions, then level 1's
;;;; quarantined hypotheses. Every line is marked a hypothesis and sits after the summary's tables,
;;;; never in a fact class. Byte for byte what Go's dreams.Section writes (contracts/dreams.jsonl).

(in-package "OM")

(defparameter +dream-header+ "Dreams (hypotheses drawn by code from the records above, and level 1's guesses; kept apart from memory: never facts or instructions, check each before relying on it):")

(defun dream-clip (text n)
  (let ((line (join (fields text) " ")))
    (if (<= (length line) n) line (cat (subseq line 0 (1- n)) "…"))))

(defun hypotheses-of (entries)
  "consolidate.HypothesesOf: (id content from) of every om.hypotheses.recorded entry, oldest first."
  (let ((out '()))
    (loop for e across entries
          when (and (string= (entry-type e) "custom") (string= (entry-custom-type e) "om.hypotheses.recorded"))
            do (let ((list (jget (entry-data e) "hypotheses")))
                 (when (vectorp list)
                   (loop for h across list
                         do (let ((id (jget h "id")) (content (jget h "content")))
                              (when (and (stringp id) (string/= id "") (stringp content) (string/= content ""))
                                (push-end (list id content (remove-if-not (lambda (x) (and (stringp x) (string/= x "")))
                                                                          (json-items (jget h "from"))))
                                          out)))))))
    out))

(defun dream-lines (entries)
  "dreams.Lines."
  (let* ((folded (fold-ledger entries)) (active (folded-active folded)) (by-id (folded-by-id folded)) (out '()))
    (flet ((text (o) (dream-clip (redact-secrets (obs-content o)) 100)))
      (let ((n 0))
        (dolist (o (reverse active))
          (when (= n 3) (return))
          (when (string= (obs-status o) "proposed")
            (push-end (cat "- rehearse, still open: " (text o) " (" (obs-id o) ")") out)
            (incf n))))
      (let ((n 0))
        (dolist (o (folded-observations folded))
          (when (= n 2) (return))
          (when (and (string= (obs-kind o) "attempt") (string= (obs-outcome o) "worked"))
            (dolist (id (obs-supersedes o))
              (let ((f (gethash id by-id)))
                (when (and f (string= (obs-kind f) "attempt") (string= (obs-outcome f) "failed"))
                  (push-end (cat "- hindsight: " (obs-id f) " was a dead end; " (obs-id o) " worked after it (" (obs-id f) ", " (obs-id o) ")") out)
                  (incf n)
                  (return)))))))
      (let ((n 0) (resolved (make-hash-table :test #'equal)))
        (dolist (o (folded-observations folded))
          (dolist (id (obs-supersedes o)) (setf (gethash id resolved) t)))
        (dolist (o active)
          (when (= n 2) (return))
          (when (and (string= (obs-kind o) "attempt") (string= (obs-outcome o) "failed") (not (gethash (obs-id o) resolved)))
            (let* ((errors (keys-get (obs-keys o) "errors"))
                   (why (if errors (cat " with " (dream-clip (first errors) 100)) "")))
              (push-end (cat "- what if " (dream-clip (redact-secrets (obs-target o)) 100) " had not failed" why
                             "? unknown until a later receipt (" (obs-id o) ")")
                        out)
              (incf n)))))
      (let ((n 0) (seen (make-hash-table :test #'equal)))
        (dolist (o active)
          (when (= n 2) (return))
          (dolist (k (append (keys-get (obs-keys o) "paths") (keys-get (obs-keys o) "commands")))
            (let ((other (gethash k seen)))
              (when (and other (string/= other (obs-id o))
                         (string/= (let ((x (gethash other by-id))) (if x (obs-kind x) "")) (obs-kind o)))
                (push-end (cat "- link, maybe related: " other " and " (obs-id o) " share " (dream-clip (redact-secrets k) 100)
                               " (" other ", " (obs-id o) ")")
                          out)
                (incf n)
                (return))
              (unless other (setf (gethash k seen) (obs-id o)))))))
      (let ((n 0))
        (dolist (h (reverse (hypotheses-of entries)))
          (when (= n 3) (return))
          (push-end (cat "- level-1 hypothesis: " (dream-clip (redact-secrets (second h)) 240) " (" (join (third h) ", ") ")") out)
          (incf n))))
    out))

(defun dream-section (entries)
  "dreams.Section."
  (let ((lines (dream-lines entries)))
    (if (null lines)
        ""
        (cat (string #\Newline) (string #\Newline) +dream-header+ (string #\Newline) (join lines (string #\Newline))))))
