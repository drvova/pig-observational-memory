;;;; memory/ledger truth.go and labels.go: truth maintenance after a fold has read every entry
;;;; (forget tombstones, decision 1's cascade, the reflection quorum; contracts/truth.md). The
;;;; labels are in labels.lisp.

(in-package "OM.KERNEL")

(defun reflection-quorum (n) "ReflectionQuorum." (floor (1+ n) 2))

(defstruct truth (forgotten (make-set)) (out (make-set)))

(defun maintain-truth (observations dropped by reflections)
  "maintainTruth: (values observations reflections truth); dropped and by change in place."
  (let ((tr (make-truth)) (sources (make-set)) (by-id (make-set)) (forgets '())
        (retracted (make-set)) (erased (make-set)))
    (dolist (o observations) (setf (gethash (obs-id o) by-id) o))
    (dolist (o observations)
      (multiple-value-bind (rep ok) (gethash (obs-id o) by)
        (when ok (push-end (obs-id o) (gethash rep sources)))))
    (labels ((merge-p (id) (let ((o (gethash id by-id)))
                             (or (and o (> (length (obs-replaces o)) 1)) (> (length (gethash id sources)) 1))))
             (forgotten (id) (gethash id (truth-forgotten tr)))
             (out (id) (gethash id (truth-out tr))))
      (dolist (o observations)
        (when (string= (obs-kind o) "forget")
          (push-end o forgets)
          (dolist (s (obs-forgets o))
            (unless (forgotten (first s)) (setf (gethash (first s) (truth-forgotten tr)) (obs-id o))))))
      (dolist (o observations)
        (unless (or (forgotten (obs-id o)) (string= (obs-kind o) "forget") (string/= (obs-origin o) "code"))
          (block tombstones
            (dolist (f forgets)
              (dolist (s (obs-forgets f))
                (destructuring-bind (id kind srcs) s
                  (declare (ignore id))
                  (when (and (string/= kind "") (string= (obs-kind o) kind) srcs (same-set-p (obs-source-ids o) srcs))
                    (setf (gethash (obs-id o) (truth-forgotten tr)) (obs-id f))
                    (return-from tombstones))))))))
      (let ((forget-ids '()))
        (maphash (lambda (id v) (declare (ignore v))
                   (let ((o (gethash id by-id))) (when (and o (string= (obs-kind o) "forget")) (push id forget-ids))))
                 (truth-forgotten tr))
        (dolist (id forget-ids) (remhash id (truth-forgotten tr))))
      (dolist (o observations)
        (unless (or (forgotten (obs-id o)) (string/= (obs-status o) +status-retracted+))
          (dolist (id (obs-supersedes o))
            (unless (or (nth-value 1 (gethash id retracted)) (string= id (obs-id o)))
              (setf (gethash id retracted) (obs-id o))))))
      (loop with changed = t
            while changed
            do (setf changed nil)
               (dolist (o observations)
                 (let* ((r (obs-id o)) (srcs (gethash r sources)) (cause (forgotten r)))
                   (cond (cause
                          (dolist (s srcs)
                            (unless (forgotten s)
                              (setf (gethash s (truth-forgotten tr)) cause changed t))))
                         ((merge-p r)
                          (unless (nth-value 1 (gethash r (truth-out tr)))
                            (dolist (s srcs)
                              (let ((c "") (erase nil))
                                (cond ((forgotten s) (setf c (forgotten s) erase t))
                                      ((nth-value 1 (gethash s retracted)) (setf c (gethash s retracted)))
                                      ((nth-value 1 (gethash s (truth-out tr))) (setf c (out s) erase (gethash s erased))))
                                (when (string/= c "")
                                  (setf (gethash r (truth-out tr)) c (gethash r erased) erase changed t)
                                  (return))))))
                         ((= (length srcs) 1)
                          (let ((s (first srcs)))
                            (cond ((forgotten s) (setf (gethash r (truth-forgotten tr)) (forgotten s) changed t))
                                  ((nth-value 1 (gethash s (truth-out tr)))
                                   (unless (nth-value 1 (gethash r (truth-out tr)))
                                     (setf (gethash r (truth-out tr)) (out s) (gethash r erased) (gethash s erased) changed t))))))))))
      (dolist (o observations)
        (let ((r (obs-id o)))
          (when (nth-value 1 (gethash r (truth-out tr)))
            (if (forgotten r)
                (remhash r (truth-out tr))
                (progn
                  (setf (gethash r dropped) t)
                  (remhash r by)
                  (dolist (s (gethash r sources))
                    (let ((gone (forgotten s)) (outp (nth-value 1 (gethash s (truth-out tr)))))
                      (remhash s by)
                      (unless (or gone outp) (remhash s dropped)))))))))
      (let ((kept '()))
        (dolist (o observations)
          (if (or (forgotten (obs-id o)) (gethash (obs-id o) erased))
              (progn (remhash (obs-id o) by) (remhash (obs-id o) dropped))
              (push o kept)))
        (setf kept (nreverse kept))
        (let ((gone '()))
          (maphash (lambda (id rep) (when (or (forgotten rep) (gethash rep erased)) (push id gone))) by)
          (dolist (id gone) (remhash id by)))
        (let ((superseder (make-set)) (kept-refl '()))
          (dolist (o kept)
            (unless (gethash (obs-id o) dropped)
              (dolist (id (obs-supersedes o))
                (unless (or (nth-value 1 (gethash id superseder)) (string= id (obs-id o)))
                  (setf (gethash id superseder) (obs-id o))))))
          (dolist (r reflections)
            (let ((n (length (refl-supporting r))) (standing 0) (cause "") (wipe nil))
              (dolist (id (refl-supporting r))
                (let ((c ""))
                  (cond ((forgotten id) (setf c (forgotten id) wipe t))
                        ((nth-value 1 (gethash id (truth-out tr))) (setf c (out id) wipe (or wipe (gethash id erased))))
                        ((nth-value 1 (gethash id retracted)) (setf c (gethash id retracted)))
                        ((nth-value 1 (gethash id superseder)) (setf c (gethash id superseder))))
                  (if (string= c "")
                      (incf standing)
                      (when (string= cause "") (setf cause c)))))
              (if (or wipe (and (> n 0) (< standing (reflection-quorum n))))
                  (setf (gethash (refl-id r) (truth-out tr)) cause)
                  (push r kept-refl))))
          (values kept (nreverse kept-refl) tr))))))


(defun folded-tombstoned-p (f o)
  "FoldedLedger.Tombstoned: a tombstone of f's forget records names o, by id or by kind and sources."
  (or (nth-value 1 (gethash (obs-id o) (truth-forgotten (folded-truth f))))
      (some (lambda (r)
              (some (lambda (s)
                      (destructuring-bind (id kind srcs) s
                        (or (string= id (obs-id o))
                            (and (string/= kind "") (string= (obs-kind o) kind) srcs (same-set-p (obs-source-ids o) srcs)))))
                    (obs-forgets r)))
            (folded-observations f))))

;;; intent.go: the types of an intention (contracts/truth.md, "Intentions")

(defparameter +intent-deferred+
  (compile-regex #r§(?i)\b(?:later|eventually|some\s?day|tomorrow|next\s+(?:week|month|sprint|quarter|session|time)|after\s+the\s+release|down\s+the\s+road|at\s+some\s+point|another\s+time)\b§))
(defparameter +intent-condition+
  (compile-regex #r§(?i)(?:^(?:once|when|after)\b|\b(?:once|as\s+soon\s+as|wait(?:ing)?\s+(?:for|until))\b)§))
(defparameter +intent-promise+
  (compile-regex #r§(?i)^(?:i'll|i\s+will|i'm\s+going\s+to|i\s+am\s+going\s+to|let\s+me)\b§))

(defun intent-of (o)
  "IntentOf: a plan's type, \"\" for any other record."
  (cond ((string/= (obs-status o) +status-proposed+) "")
        ((regex-match-p +intent-deferred+ (obs-content o)) "deferred")
        ((regex-match-p +intent-condition+ (obs-content o)) "condition-wait")
        ((or (string= (obs-kind o) "assistant") (regex-match-p +intent-promise+ (obs-content o))) "promise")
        (t "todo")))

;;; decay.go: decay lowers rank only (contracts/truth.md, "Decay"; decision 6)

(defparameter +default-decay-horizon+ 4)

(defun decay-protected-p (o superseded)
  "DecayProtected."
  (or (user-priority-p o) (string= (obs-kind o) "correction")
      (and (string= (obs-kind o) "attempt") (string= (obs-outcome o) "failed") (not (gethash (obs-id o) superseded)))
      (search "[redacted:" (obs-content o)) (search "[redacted:" (obs-target o))))

;; Decay is a fold over the branch (ledger.DecayFold): every part of its state is a position or a
;; maximum of positions, so advancing a checkpoint over the entries after it gives what one fold over
;; the whole branch gives (the semigroup property; tests/truth.lisp, decay-composes). An event is
;; what the branch recorded: a later drop, merge or forget of the record that carried it does not
;; undo it.
(defstruct (decay-fold (:copier nil))
  (through 0) (positions (make-set)) (sources (make-set)) (linked (make-set)) (latest (make-set))
  ;; The ticks of each clock, newest first.
  (compactions '()) (turns '()))

(defun copy-hash-set (h) (let ((out (make-set))) (maphash (lambda (k v) (setf (gethash k out) v)) h) out))

(defun decay-fold-clone (f)
  "DecayFold.Clone."
  (make-decay-fold :through (decay-fold-through f) :positions (copy-hash-set (decay-fold-positions f))
                   :sources (copy-hash-set (decay-fold-sources f)) :linked (copy-hash-set (decay-fold-linked f))
                   :latest (copy-hash-set (decay-fold-latest f))
                   :compactions (copy-list (decay-fold-compactions f)) :turns (copy-list (decay-fold-turns f))))

(defun decay-fold-at (f o)
  (let ((n -1))
    (dolist (id (obs-source-ids o) n)
      (let ((i (gethash id (decay-fold-positions f)))) (when (and i (> i n)) (setf n i))))))

(defun decay-fold-advance (f entries)
  "DecayFold.Advance: f after entries (a vector), the entries that follow what it read."
  (flet ((raise (table key n)
           (multiple-value-bind (v ok) (gethash key table) (when (or (not ok) (> n v)) (setf (gethash key table) n)))))
    (loop for e across entries
          do (let ((i (decay-fold-through f)))
               (incf (decay-fold-through f))
               (setf (gethash (entry-id e) (decay-fold-positions f)) i)
               (cond ((string= (entry-type e) "compaction") (push i (decay-fold-compactions f)))
                     ((and (string= (entry-type e) "message") (entry-message e) (equal (jget (entry-message e) "role") "user"))
                      (push i (decay-fold-turns f)))
                     (t (multiple-value-bind (list covers) (recorded-data e)
                          (when covers
                            (dolist (r list)
                              (unless (nth-value 1 (gethash (obs-id r) (decay-fold-sources f)))
                                (setf (gethash (obs-id r) (decay-fold-sources f)) t)
                                (when (string= (obs-origin r) "code")
                                  (let ((n (decay-fold-at f r)))
                                    (dolist (id (append (obs-supersedes r) (obs-possibly-updates r) (obs-conflicts r) (obs-responds-to r)
                                                        (mapcar #'first (obs-closes r))))
                                      (unless (string= id (obs-id r)) (raise (decay-fold-linked f) id n)))
                                    (when (member (obs-source r) '("user-said" "tool-observed") :test #'string=)
                                      (dolist (kind +key-kinds+)
                                        (dolist (k (keys-get (obs-keys r) kind))
                                          (raise (decay-fold-latest f) (cat kind (string (code-char 0)) k) n)))))))))))))))
  f)

(defun decay-fold-evidence (f records replaced-by)
  "DecayFold.Evidence: id -> the branch index of each record's newest evidence event, a link read
through replaced-by to the record standing for its target."
  (let ((out (make-set)) (targets '()))
    (flet ((raise (id n) (multiple-value-bind (v ok) (gethash id out) (when (and ok (> n v)) (setf (gethash id out) n)))))
      (dolist (o records) (setf (gethash (obs-id o) out) (decay-fold-at f o)))
      (maphash (lambda (k v) (declare (ignore v)) (push k targets)) (decay-fold-linked f))
      (dolist (id (sort targets #'string<))
        (raise (standing id replaced-by) (gethash id (decay-fold-linked f))))
      (dolist (o records out)
        (dolist (kind +key-kinds+)
          (dolist (k (keys-get (obs-keys o) kind))
            (multiple-value-bind (n ok) (gethash (cat kind (string (code-char 0)) k) (decay-fold-latest f))
              (when ok (raise (obs-id o) n)))))))))

(defun decay-fold-decayed (f records replaced-by clock horizon)
  "DecayFold.Decayed: a set of the ids of records that decay on clock with horizon."
  (let ((out (make-set)) (superseded (make-set)))
    (when (or (not (member clock '("compactions" "turns") :test #'equal)) (<= horizon 0))
      (return-from decay-fold-decayed out))
    (dolist (o records)
      (dolist (id (obs-supersedes o)) (unless (string= id (obs-id o)) (setf (gethash id superseded) t))))
    (let ((ticks (if (string= clock "compactions") (decay-fold-compactions f) (decay-fold-turns f)))
          (evidence (decay-fold-evidence f records replaced-by)))
      (dolist (o records out)
        (unless (decay-protected-p o superseded)
          (let ((n (gethash (obs-id o) evidence)))
            (when (>= (count-if (lambda (tk) (> tk n)) ticks) horizon)
              (setf (gethash (obs-id o) out) t))))))))

(defun decay-evidence (entries records)
  "DecayEvidence."
  (decay-fold-evidence (decay-fold-advance (make-decay-fold) entries) records (folded-replaced-by (fold-ledger entries))))

(defun decay-ticks (entries clock)
  "DecayTicks."
  (loop for e across entries for i from 0
        when (cond ((string= clock "compactions") (string= (entry-type e) "compaction"))
                   ((string= clock "turns") (and (string= (entry-type e) "message") (entry-message e)
                                                 (equal (jget (entry-message e) "role") "user"))))
          collect i))

(defun decayed-records (entries records clock horizon)
  "Decayed: a set of the ids of records that decay on clock with horizon."
  (if (or (not (member clock '("compactions" "turns") :test #'equal)) (<= horizon 0))
      (make-set)
      (decay-fold-decayed (decay-fold-advance (make-decay-fold) entries) records
                          (folded-replaced-by (fold-ledger entries)) clock horizon)))

