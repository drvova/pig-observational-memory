;;;; The sleep pass against its contract cases: contracts/sleep.jsonl is memory/sleep's output
;;;; (contract_test.go), which sleep-pass here must write byte for byte, with the same hidden
;;;; records and labels once its record is on the branch.

(in-package "OM")

(deftest sleep-contract-cases
  (let ((cases (contract-cases "sleep.jsonl")))
    (check (> (length cases) 6) t "cases read")
    (dolist (c cases)
      (let* ((name (jget c "name"))
             (entries (map 'vector #'entry-from-map (jget c "entries")))
             (decay (or (jget c "decay") ""))
             (horizon (truncate (or (jget c "decayHorizon") 0)))
             (got (sleep-pass entries (jget c "trigger") :decay decay :decay-horizon horizon
                                                         :repo (jget c "repo") :structure (jget c "structure"))))
        (check got (jget c "json") (cat name ": json"))
        (check (compactions-since-sleep entries) (truncate (jget c "due")) (cat name ": due"))
        (when got
          (let* ((after (concatenate 'simple-vector entries (vector (synthetic-entry "s-new" "om.sleep" got))))
                 (f (fold-ledger after))
                 (hidden (sleep-hidden after)))
            (check (loop for o in (folded-active f) when (and hidden (gethash (obs-id o) hidden)) collect (obs-id o))
                   (truth-strings (jget c "hidden")) (cat name ": hidden"))
            (check (truth-alist (folded-labels f)) (truth-alist (jget c "labels")) (cat name ": labels"))))))))

(deftest sleep-fallback-in-a-compaction
  ;; The fallback runs once K compactions passed with no sleep record, appends one om.sleep entry
  ;; through the host and leaves it out at K = 0.
  (let* ((branch (vector (entry-from-map (json-parse "{\"type\":\"message\",\"id\":\"u1\",\"message\":{\"role\":\"user\",\"content\":\"hi\"}}"))
                         (entry-from-map (json-parse "{\"type\":\"compaction\",\"id\":\"c1\",\"summary\":\"s\",\"firstKeptEntryId\":\"u1\",\"tokensBefore\":1}"))
                         (entry-from-map (json-parse "{\"type\":\"compaction\",\"id\":\"c2\",\"summary\":\"s\",\"firstKeptEntryId\":\"u1\",\"tokensBefore\":1}"))))
         (appended '())
         (h (make-hhost :read (lambda () branch)
                        :append (lambda (type data) (push (cons type data) appended) "new"))))
    (check (length (sleep-fallback h (make-hconfig :sleep-after 3) branch)) 3 "not due")
    (check appended '() "nothing appended")
    (let ((out (sleep-fallback h (make-hconfig :sleep-after 2) branch)))
      (check (length out) 4 "due")
      (check (mapcar #'car appended) '("om.sleep") "one sleep record")
      (check (compactions-since-sleep out) 0 "count reset"))
    (setf appended '())
    (check (length (sleep-fallback h (make-hconfig :sleep-after 0) branch)) 3 "K = 0")
    (check (length (sleep-fallback h (make-hconfig :sleep-after 2 :sleep-max-records 0) branch)) 4 "no bound")))
