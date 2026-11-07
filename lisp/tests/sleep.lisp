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

(defun jstrs-of (v) (if (and (vectorp v) (not (stringp v))) (coerce v 'list) '()))

(deftest sleep-all-over-the-stores
  ;; The idle child's pass (server/sleep.lisp): a store gets one om.sleep line, the state file
  ;; checkpoints it, an unchanged store is skipped, and a held lock makes the pass stand down.
  (let* ((dir (sb-posix:mkdtemp (format nil "~A/om-sleep-XXXXXX" (or (sb-posix:getenv "TMPDIR") "/tmp"))))
         (store (cat dir "/sessions/generic/s1.jsonl"))
         (c (first (contract-cases "sleep.jsonl")))
         (prev ""))
    (unwind-protect
         (progn
           (om.server::ensure-parent store)
           (with-open-file (out store :direction :output :if-exists :supersede :external-format :utf-8)
             (loop for e across (jget c "entries")
                   do (write-line (stored-line prev e) out)
                      (setf prev (jget e "id"))))
           (check (om.server::sleep-all dir "manual") 1 "one store slept")
           (let* ((branch (coerce (neutral-branch '() (store-load store)) 'simple-vector))
                  (last (aref branch (1- (length branch)))))
             (check (entry-custom-type last) "om.sleep" "appended at the tip")
             (check (jget (entry-data last) "trigger") "manual" "its trigger")
             (check (length (jstrs-of (jget (entry-data last) "duplicates"))) 3 "its duplicates"))
           (check (om.server::sleep-all dir "manual") 0 "unchanged store skipped")
           (check (and (probe-file (cat dir "/sleep/state.json")) t) t "checkpointed")
           (let ((fd (om.server::lock-file (cat dir "/sleep/.lock"))))
             (unwind-protect (check (om.server::sleep-all dir "manual") :busy "lock held")
               (om.server::unlock-file fd))))
      (sb-ext:delete-directory (cat dir "/") :recursive t))))
