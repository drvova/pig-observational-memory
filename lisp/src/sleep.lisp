;;;; The NREM sleep pass (memory/sleep/sleep.go, memory/ledger/sleep.go; docs/memory-v2.md,
;;;; "Sleep"; docs/brain-memory.md, decision 13): one code-only pass over a session's ledger that
;;;; makes no model request. It finds duplicates (by hash, and evidence by normalized error lines),
;;;; links the records sharing a key, notes what decay lowers when decay is on, verifies the
;;;; project records against the HEAD structure view and checks level 1's supersedes and lessons
;;;; against the evidence after them. Its result is one om.sleep entry's data, written as Go's
;;;; json.Marshal writes ledger.SleepData, so both engines append the same bytes
;;;; (contracts/sleep.jsonl).

(in-package "OM.ENGINE")

(defparameter +sleep-max-duplicates+ 256)
(defparameter +sleep-max-links+ 64)
(defparameter +sleep-max-link-group+ 8)
(defparameter +sleep-max-checks+ 64)
(defparameter +sleep-max-by+ 4)

;;; The sleep records on a branch (ledger.SleepState)

(defstruct sstate* (runs 0) (duplicates (make-set)) (checks (make-set)) (verified (make-set))
  (retired (make-set)) (decayed (make-set)) (links (make-set)) (seen (make-set)) (last nil))

(defun sleep-link-key (key ids)
  (apply #'cat key (loop for id in ids collect (cat (string #\Nul) id))))

(defun jstrs (v) (if (and (vectorp v) (not (stringp v))) (remove-if-not #'stringp (coerce v 'list)) '()))

(defun sleep-state-of (entries)
  "SleepStateOf: every sleep record of entries folded."
  (let ((st (make-sstate*)))
    (loop for e across entries
          for d = (sleep-data e)
          when d
            do (incf (sstate*-runs st))
               (setf (sstate*-last st) d)
               (dolist (x (sleep-items d "duplicates"))
                 (let ((id (str (jget x "id"))))
                   (unless (nth-value 1 (gethash id (sstate*-duplicates st)))
                     (setf (gethash id (sstate*-duplicates st)) x))))
               (dolist (c (sleep-items d "checks")) (setf (gethash (str (jget c "id")) (sstate*-checks st)) c))
               (dolist (v (sleep-items d "verified")) (setf (gethash (str (jget v "id")) (sstate*-verified st)) v))
               (dolist (r (sleep-items d "retired"))
                 (let ((id (str (jget r "id"))))
                   (unless (nth-value 1 (gethash id (sstate*-retired st)))
                     (setf (gethash id (sstate*-retired st)) r))))
               (dolist (id (jstrs (jget d "decayed"))) (setf (gethash id (sstate*-decayed st)) t))
               (dolist (l (sleep-items d "links"))
                 (let ((k (sleep-link-key (str (jget l "key")) (jstrs (jget l "ids")))))
                   (unless (gethash k (sstate*-seen st))
                     (setf (gethash k (sstate*-seen st)) t)
                     (dolist (id (jstrs (jget l "ids"))) (push-end l (gethash id (sstate*-links st))))))))
    st))

(defun sleep-indexes (entries)
  "The entry indexes of the valid sleep records of entries (Sleeps)."
  (loop for e across entries for i from 0 when (sleep-data e) collect i))

(defun compactions-since-sleep (entries)
  "CompactionsSinceSleep: the compactions after the latest sleep record."
  (loop for i from (1- (length entries)) downto 0
        for e = (aref entries i)
        until (sleep-data e)
        count (string= (entry-type e) "compaction")))

(defun sleep-hidden (entries)
  "hooks.SleepHidden: the ids the sleep records hid as duplicates, nil when none."
  (let ((d (sstate*-duplicates (sleep-state-of entries))))
    (when (plusp (hash-table-count d))
      (let ((out (make-set))) (maphash (lambda (k v) (declare (ignore v)) (setf (gethash k out) t)) d) out))))

;;; The pass

(defun sleep-normalize (s)
  "sleep.Normalize: ASCII letters lowered, white space runs one space, trimmed."
  (let ((out (join (fields s) " ")))
    (map 'string (lambda (c) (if (char<= #\A c #\Z) (code-char (+ (char-code c) 32)) c)) out)))

(defun sleep-key-values (o)
  "keyValues: a record's keys as kind:value, in +key-kinds+ order."
  (loop for kind in +key-kinds+ nconc (loop for v in (keys-get (obs-keys o) kind) collect (cat kind ":" v))))

(defun sleep-referenced (f)
  (let ((out (make-set)))
    (flet ((add (ids) (dolist (id ids) (setf (gethash id out) t))))
      (dolist (o (folded-observations f))
        (add (obs-supersedes o)) (add (obs-possibly-updates o)) (add (obs-conflicts o))
        (add (obs-replaces o)) (add (obs-responds-to o))
        (dolist (c (obs-closes o)) (setf (gethash (first c) out) t))
        (dolist (tb (obs-forgets o)) (setf (gethash (first tb) out) t)))
      (dolist (r (folded-reflections f)) (add (refl-supporting r))))
    out))

(defun sleep-eligible-p (o labels refs)
  (and (equal (gethash (obs-id o) labels) "believed") (not (gethash (obs-id o) refs))
       (string/= (obs-kind o) "forget") (string/= (obs-origin o) "host")
       (not (decay-protected-p o (make-set)))
       (null (obs-supersedes o)) (null (obs-possibly-updates o)) (null (obs-conflicts o)) (null (obs-replaces o))
       (null (obs-responds-to o)) (null (obs-closes o)) (null (obs-forgets o))))

(defun sleep-duplicates (records labels f st)
  "duplicates: (id of by) lists, every record after the first of its group a duplicate of it."
  (let* ((refs (sleep-referenced f))
         (candidates (remove-if-not (lambda (o) (sleep-eligible-p o labels refs)) records))
         (out '()) (taken (make-set)))
    (flet ((group (by key)
             (let ((first (make-set)))
               (dolist (o candidates)
                 (unless (gethash (obs-id o) taken)
                   (let ((k (funcall key o)))
                     (when (and (string/= k "") (not (nth-value 1 (gethash k first))))
                       (setf (gethash k first) (obs-id o))))))
               (dolist (o candidates)
                 (unless (gethash (obs-id o) taken)
                   (let ((k (funcall key o)))
                     (unless (or (string= k "") (string= (gethash k first) (obs-id o)))
                       (setf (gethash (obs-id o) taken) t)
                       (unless (or (nth-value 1 (gethash (obs-id o) (sstate*-duplicates st)))
                                   (>= (length out) +sleep-max-duplicates+))
                         (push-end (list (obs-id o) (gethash k first) by) out)))))))))
      (group "hash" (lambda (o) (cat (obs-kind o) (string #\Nul) (obs-status o) (string #\Nul) (obs-subject o)
                                     (string #\Nul) (obs-scope o) (string #\Nul) (obs-content o))))
      (group "error" (lambda (o)
                       (if (string/= (obs-kind o) "evidence")
                           ""
                           (let ((lines (error-lines (obs-content o) t)))
                             (if lines (cat (obs-tool o) (string #\Nul) (join lines (string #\Newline))) ""))))))
    out))

(defun sleep-links (records labels hidden st)
  "links: (key ids) lists of the keys two to +sleep-max-link-group+ shown records share."
  (let ((by (make-set)) (out '()))
    (dolist (o records)
      (when (and (equal (gethash (obs-id o) labels) "believed") (not (gethash (obs-id o) hidden)))
        (dolist (k (sleep-key-values o))
          (let ((ids (gethash k by)))
            (when (or (null ids) (string/= (car (last ids)) (obs-id o)))
              (setf (gethash k by) (append ids (list (obs-id o)))))))))
    (let ((keys '()))
      (maphash (lambda (k v) (declare (ignore v)) (push k keys)) by)
      (dolist (k (sort keys #'string<) out)
        (let ((ids (gethash k by)))
          (when (and (<= 2 (length ids) +sleep-max-link-group+)
                     (not (gethash (sleep-link-key k ids) (sstate*-seen st))))
            (when (>= (length out) +sleep-max-links+) (return out))
            (push-end (list k ids) out)))))))

(defun sleep-verify (records repo answer st)
  "verify: (values verified retired), each a list of (id head verdict-or-reason)."
  (let ((head (str (jget answer "head"))) (verdicts (make-set)) (verified '()) (retired '()))
    (when (or (string= head "") (string= (str (jget repo "repoId")) ""))
      (return-from sleep-verify (values '() '())))
    (dolist (v (project-verdicts records repo answer)) (setf (gethash (freshness-id v) verdicts) (freshness-kind v)))
    (flet ((same-p (id verdict)
             (let ((prev (gethash id (sstate*-verified st))))
               (and prev (equal (jget prev "head") head) (equal (jget prev "verdict") verdict)))))
      (dolist (o (project-anchors records repo))
        (let ((id (obs-id o)) (kind (gethash (obs-id o) verdicts)))
          (cond ((equal kind "gone")
                 (unless (nth-value 1 (gethash id (sstate*-retired st)))
                   (push-end (list id head "gone") retired)))
                ((equal kind "changed")
                 (unless (same-p id "changed") (push-end (list id head "changed") verified)))
                ((nth-value 1 (gethash id (sstate*-retired st))))
                (t (unless (same-p id "holds") (push-end (list id head "holds") verified)))))))
    (values verified retired)))

(defun sleep-positions (entries)
  (let ((out (make-set)))
    (loop for e across entries for i from 0
          do (when (string= (entry-type e) "custom")
               (cond ((equal (entry-custom-type e) "om.observations.recorded")
                      (multiple-value-bind (list covers) (recorded-data e)
                        (when covers
                          (dolist (o list) (unless (nth-value 1 (gethash (obs-id o) out)) (setf (gethash (obs-id o) out) i))))))
                     ((equal (entry-custom-type e) "om.reflections.recorded")
                      (multiple-value-bind (list covers) (reflections-data e)
                        (when covers
                          (dolist (r list) (unless (nth-value 1 (gethash (refl-id r) out)) (setf (gethash (refl-id r) out) i)))))))))
    out))

(defun sleep-checks (entries f st)
  "checks: (id kind verdict by) lists."
  (let* ((pos (sleep-positions entries))
         (level1 (consolidated-observation-ids entries))
         (later (remove-if-not (lambda (o) (and (not (gethash (obs-id o) level1)) (string= (obs-origin o) "code")))
                               (folded-active f)))
         (out '()))
    (labels ((after (id)
               (multiple-value-bind (at ok) (gethash id pos)
                 (when ok
                   (remove-if-not (lambda (o) (multiple-value-bind (p ok2) (gethash (obs-id o) pos) (and ok2 (> p at)))) later))))
             (shares (o keys) (some (lambda (k) (gethash k keys)) (sleep-key-values o)))
             (failed-attempt-p (e) (and (string= (obs-kind e) "attempt") (string= (obs-outcome e) "failed")))
             (add (id kind evidence challenge)
               (when (and evidence (< (length out) +sleep-max-checks+))
                 (let ((verdict "holds") (by '()))
                   (dolist (e evidence)
                     (when (funcall challenge e)
                       (setf verdict "challenged")
                       (when (< (length by) +sleep-max-by+) (push-end (obs-id e) by))))
                   (let ((prev (gethash id (sstate*-checks st))))
                     (unless (and prev (equal (jget prev "verdict") verdict))
                       (push-end (list id kind verdict by) out)))))))
      (dolist (o (folded-active f))
        (when (gethash (obs-id o) level1)
          (let ((targets (loop for id in (append (obs-replaces o) (obs-supersedes o))
                               for tg = (gethash id (folded-by-id f))
                               when (and tg (string/= id (obs-id o))) collect tg))
                (keys (make-set)))
            (when targets
              (dolist (k (sleep-key-values o)) (setf (gethash k keys) t))
              (add (obs-id o) "supersede" (after (obs-id o))
                   (lambda (e)
                     (or (some (lambda (tg) (string= (sleep-normalize (obs-content e)) (sleep-normalize (obs-content tg)))) targets)
                         (and (failed-attempt-p e) (shares e keys)))))))))
      (dolist (r (active-reflections (folded-reflections f)))
        (let ((keys (make-set)))
          (dolist (id (refl-supporting r))
            (let ((s (gethash id (folded-by-id f))))
              (when s (dolist (k (sleep-key-values s)) (setf (gethash k keys) t)))))
          (when (plusp (hash-table-count keys))
            (add (refl-id r) "lesson" (after (refl-id r))
                 (lambda (e) (and (or (failed-attempt-p e) (string= (obs-kind e) "evidence")) (shares e keys))))))))
    out))

(defun sleep-data-json (trigger through records duplicates links decayed verified retired checks)
  "ledger.SleepData as json.Marshal writes it: its fields in order, empty lists left out."
  (with-output-to-string (s)
    (format s "{\"version\":1,\"trigger\":~A,\"throughEntryId\":~A,\"records\":~D"
            (json-string trigger) (json-string through) records)
    (flet ((list-field (name items fn)
             (when items
               (format s ",\"~A\":[" name)
               (loop for x in items for first = t then nil
                     do (unless first (write-char #\, s)) (funcall fn x))
               (write-char #\] s))))
      (list-field "duplicates" duplicates
                  (lambda (d) (format s "{\"id\":~A,\"of\":~A,\"by\":~A}" (json-string (first d)) (json-string (second d)) (json-string (third d)))))
      (list-field "links" links
                  (lambda (l) (format s "{\"key\":~A,\"ids\":~A}" (json-string (first l)) (json-string (coerce (second l) 'simple-vector)))))
      (list-field "decayed" decayed (lambda (id) (write-string (json-string id) s)))
      (list-field "verified" verified
                  (lambda (v) (format s "{\"id\":~A,\"head\":~A,\"verdict\":~A}" (json-string (first v)) (json-string (second v)) (json-string (third v)))))
      (list-field "retired" retired
                  (lambda (v) (format s "{\"id\":~A,\"head\":~A,\"reason\":~A}" (json-string (first v)) (json-string (second v)) (json-string (third v)))))
      (list-field "checks" checks
                  (lambda (c) (format s "{\"id\":~A,\"kind\":~A,\"verdict\":~A~@[,\"by\":~A~]}"
                                      (json-string (first c)) (json-string (second c)) (json-string (third c))
                                      (and (fourth c) (json-string (coerce (fourth c) 'simple-vector)))))))
    (write-char #\} s)))

(defun sleep-pass (entries trigger &key (decay "") (decay-horizon 0) repo structure)
  "sleep.Pass: the om.sleep data's JSON text for one pass over entries (a vector), nil when it is
empty. repo and structure are the decoded host.Repo and the view's answer, or nil."
  (when (zerop (length entries)) (return-from sleep-pass nil))
  (let* ((f (fold-ledger entries))
         (st (sleep-state-of entries))
         (records (folded-active f))
         (labels (folded-labels f))
         (duplicates (sleep-duplicates records labels f st))
         (hidden (make-set)))
    (maphash (lambda (k v) (declare (ignore v)) (setf (gethash k hidden) t)) (sstate*-duplicates st))
    (dolist (d duplicates) (setf (gethash (first d) hidden) t))
    (let ((links (sleep-links records labels hidden st))
          (decayed (when (string/= decay "")
                     (let ((set (decayed-records entries records decay (if (plusp decay-horizon) decay-horizon +default-decay-horizon+))))
                       (loop for o in records
                             when (and (gethash (obs-id o) set) (not (gethash (obs-id o) (sstate*-decayed st))))
                               collect (obs-id o))))))
      (multiple-value-bind (verified retired) (if structure (sleep-verify records repo structure st) (values '() '()))
        (sleep-data-json trigger (entry-id (aref entries (1- (length entries)))) (length records)
                         duplicates links decayed verified retired (sleep-checks entries f st))))))
