;;;; The promise syndrome (memory/ledger/promise.go; docs/memory-v2.md, "The promise syndrome"; the
;;;; quantum report's change 7): each records summary promises a set of protected records, shown or
;;;; listed by a printed snippet, and its compaction's details carry a digest over that set. At the
;;;; next compaction and in the sleep pass the set is worked out again from the ledger and read back
;;;; from the summary's text: a protected record the ledger says the summary promised that its text
;;;; neither shows nor lists is a located loss, named by id. Written as Go's json.Marshal writes
;;;; ledger.PromiseSyndrome and PromiseCheck, so both engines store the same bytes.

(in-package "OM.ENGINE")

(defparameter +max-promise-checks+ 32)

(defun promise-class (o contested closed superseded)
  "PromiseClass: held, contested, rule, decision, plan or nil."
  (cond ((let ((h (held-rule o))) (and h (string/= h ""))) "held")
        ((gethash (obs-id o) contested) "contested")
        ((user-priority-p o) "rule")
        ((string= (obs-kind o) "decision") "decision")
        ((and (string= (obs-status o) +status-proposed+) (not (gethash (obs-id o) superseded))
              (not (nth-value 1 (gethash (obs-id o) closed))))
         "plan")))

(defun promised (records all)
  "Promised: (obs . class) for each protected record of records, in order."
  (let ((contested (make-set)) (superseded (make-set)) (closed (closed-by all)))
    (maphash (lambda (id by) (setf (gethash id contested) t) (dolist (p by) (setf (gethash p contested) t)))
             (contested all))
    (dolist (o all)
      (dolist (id (obs-supersedes o)) (unless (string= id (obs-id o)) (setf (gethash id superseded) t))))
    (loop for o in records
          for class = (promise-class o contested closed superseded)
          when class collect (cons o class))))

(defun promise-state (p shown)
  (cond ((string= (cdr p) "held") "held")
        ((gethash (obs-id (car p)) shown) "shown")
        (t "listed")))

(defun shown-set (ids) (let ((s (make-set))) (dolist (id ids s) (setf (gethash id s) t))))

(defun promise-digest (promised shown)
  "PromiseDigest: hash-id of the sorted lines \"<id> <class> <state> <keys>\"."
  (hash-id (join (sort (loop for p in promised
                             collect (format nil "~A ~A ~A ~A" (obs-id (car p)) (cdr p) (promise-state p shown)
                                             (join (sort (sleep-key-values (car p)) #'string<) ",")))
                       #'string<)
                 (string #\Newline))))

(defun promise-check-json (c)
  "PromiseCheck as json.Marshal writes it; c is (compaction lost leaked mismatch unread)."
  (destructuring-bind (compaction lost leaked mismatch unread) c
    (format nil "{\"compaction\":~A~@[,\"lost\":~A~]~@[,\"leaked\":~A~]~:[~;,\"mismatch\":true~]~:[~;,\"unread\":true~]}"
            (json-string compaction)
            (and lost (json-string (coerce lost 'simple-vector)))
            (and leaked (json-string (coerce leaked 'simple-vector)))
            mismatch unread)))

(defun promise-syndrome-json (promised shown-ids &optional check)
  "NewPromiseSyndrome as json.Marshal writes it, check a promise-check or nil."
  (let* ((shown (shown-set shown-ids))
         (n-shown (count-if (lambda (p) (string= (promise-state p shown) "shown")) promised))
         (held (count-if (lambda (p) (string= (promise-state p shown) "held")) promised)))
    (format nil "{\"version\":1,\"records\":~D,\"shown\":~D~:[~*~;,\"held\":~D~],\"digest\":~A~@[,\"check\":~A~]}"
            (length promised) n-shown (plusp held) held (json-string (promise-digest promised shown))
            (and check (promise-check-json check)))))

(defun promise-syndrome-of (details)
  "PromiseSyndromeOf: the stored syndrome object, or nil."
  (when (hash-table-p details)
    (let ((p (jget details "promise")))
      (when (and (hash-table-p p) (realp (jget p "version")) (= (jget p "version") 1)
                 (non-empty-string-p (jget p "digest")))
        p))))

(defun promise-check-from (m)
  "promiseCheckFrom: a promise-check of a decoded object, or nil."
  (when (and (hash-table-p m) (non-empty-string-p (jget m "compaction")))
    (flet ((strs (v) (if (json-array-p v) (remove-if-not #'stringp (coerce v 'list)) '())))
      (list (jget m "compaction") (strs (jget m "lost")) (strs (jget m "leaked"))
            (eq (jget m "mismatch") :true) (eq (jget m "unread") :true)))))

(defun promised-at (branch at)
  "PromisedAt: the protected records the compaction at index at promised, from the ledger before it."
  (let ((prefix (subseq branch 0 at)))
    (promised (projection-observations (full-projection* prefix (entry-first-kept-id (aref branch at))))
              (folded-observations (fold-ledger prefix)))))

;;; Reading a summary back (readPromises)

(defun promise-row-cells (line start)
  "A rows codec row's cells from start, or :error."
  (let ((i start) (n (length line)) (out '()))
    (loop
      (if (and (< i n) (char= (char line i) #\"))
          (let ((buf (make-string-output-stream)) (closed nil))
            (incf i)
            (loop while (< i n)
                  do (let ((c (char line i)))
                       (cond ((char= c #\") (incf i) (setf closed t) (return))
                             ((char= c #\\)
                              (when (>= (1+ i) n) (return-from promise-row-cells :error))
                              (let ((e (case (char line (1+ i)) (#\" #\") (#\\ #\\) (#\n #\Newline) (#\r #\Return) (#\t #\Tab))))
                                (unless e (return-from promise-row-cells :error))
                                (write-char e buf))
                              (incf i 2))
                             (t (write-char c buf) (incf i)))))
            (unless closed (return-from promise-row-cells :error))
            (push (get-output-stream-string buf) out))
          (let ((end (or (position #\, line :start i) n)))
            (push (subseq line i end) out)
            (setf i end)))
      (cond ((>= i n) (return))
            ((char= (char line i) #\,) (incf i) (when (>= i n) (push "" out) (return)))
            (t (return-from promise-row-cells :error))))
    (nreverse out)))

(defun promise-rows-header (line)
  "(values name fields count) of a rows table header \"name[N]{f1,f2}:\", or nil."
  (let* ((open (position #\[ line)) (close (and open (position #\] line :start open)))
         (brace (and close (< (1+ close) (length line)) (char= (char line (1+ close)) #\{) (1+ close)))
         (end (and brace (position #\} line :start brace))))
    (when (and end (plusp open) (= (+ end 2) (length line)) (char= (char line (1+ end)) #\:))
      (let ((count (ignore-errors (parse-integer line :start (1+ open) :end close))))
        (when count
          (values (subseq line 0 open) (split (subseq line (1+ brace) end) ",") count))))))

(defun promise-decode-rows (body)
  "The tables of a rows-codec body: a list of (name fields rows), or :error."
  (let ((lines (mapcar (lambda (l) (string-right-trim '(#\Return) l)) (split body (string #\Newline))))
        (out '()) (current nil))
    (dolist (line lines)
      (cond ((string= line "") (setf current nil))
            ((has-prefix line "  ")
             (unless current (return-from promise-decode-rows :error))
             (let ((cells (promise-row-cells line 2)))
               (when (or (eq cells :error) (/= (length cells) (length (second current))))
                 (return-from promise-decode-rows :error))
               (push-end cells (third current))))
            (t (multiple-value-bind (name fields count) (promise-rows-header line)
                 (unless name (return-from promise-decode-rows :error))
                 (when current (push current out))
                 (setf current (list name fields '() count))))))
    (when current (push current out))
    (setf out (nreverse out))
    (if (every (lambda (tb) (= (length (third tb)) (fourth tb))) out)
        (mapcar (lambda (tb) (list (first tb) (second tb) (third tb))) out)
        :error)))

(defun promise-decode-jsonl (body)
  "The tables of a jsonl-codec body, or :error."
  (let ((out '()) (current nil))
    (dolist (raw (split body (string #\Newline)))
      (let ((line (trim-space (string-right-trim '(#\Return) raw))))
        (unless (string= line "")
          (let ((v (ignore-errors (json-parse line))))
            (cond ((and (hash-table-p v) (stringp (jget v "table")) (json-array-p (jget v "fields")))
                   (when current (push current out))
                   (setf current (list (jget v "table") (coerce (jget v "fields") 'list) '())))
                  ((and (json-array-p v) current (= (length v) (length (second current))))
                   (push-end (map 'list (lambda (c) (if (stringp c) c "")) v) (third current)))
                  (t (return-from promise-decode-jsonl :error)))))))
    (when current (push current out))
    (nreverse out)))

(defstruct ptext (ids (make-set)) (constraints '()) (spans '()) (also-rules nil) (also-plans nil))

(defun read-promises (summary codec format)
  "readPromises: a ptext, or nil for a summary the check does not read."
  (multiple-value-bind (body ok) (if (stringp summary) (tables-summary-body summary) (values "" nil))
    (when (and ok (string= format ""))
      (let ((decoded (cond ((member codec '("" "rows") :test #'string=) (promise-decode-rows body))
                           ((string= codec "jsonl") (promise-decode-jsonl body))
                           (t :error))))
        (unless (eq decoded :error)
          (let ((tx (make-ptext)))
            (dolist (tb decoded)
              (destructuring-bind (name fields rows) tb
                (flet ((col (f) (position f fields :test #'string=)))
                  (cond ((string= name "constraints")
                         (let ((c (col "content")) (s (col "source")))
                           (when (and c s) (dolist (r rows) (push-end (list (nth c r) (nth s r)) (ptext-constraints tx))))))
                        ((string= name "omitted")
                         (let ((f (col "first_id")) (l (col "last_id")))
                           (when (and f l) (dolist (r rows) (push-end (list (nth f r) (nth l r)) (ptext-spans tx))))))
                        (t (let ((i (col "id"))) (when i (dolist (r rows) (setf (gethash (nth i r) (ptext-ids tx)) t)))))))))
            (dolist (line (split summary (string #\Newline)))
              (when (has-prefix line "also: ")
                (let ((rest (subseq line 6)))
                  (when (search " more constraints: " rest) (setf (ptext-also-rules tx) t))
                  (when (search " more done: " rest) (setf (ptext-also-plans tx) t)))))
            tx))))))

(defun ptext-state (tx o positions)
  "promiseText.state: shown, listed or \"\"."
  (when (gethash (obs-id o) (ptext-ids tx)) (return-from ptext-state "shown"))
  (multiple-value-bind (anchor at anchored) (observation-anchor o positions)
    (let* ((source (if anchored anchor ""))
           (content (resolve-relative-dates (obs-content o) (record-anchor-day (obs-timestamp o)))))
      (when (some (lambda (row) (and (string= (second row) source)
                                     (or (has-prefix (first row) content) (has-prefix (first row) (redact-secrets content)))))
                  (ptext-constraints tx))
        (return-from ptext-state "shown"))
      (dolist (span (ptext-spans tx))
        (let ((lo (gethash (first span) positions)) (hi (gethash (second span) positions)))
          (when (or (and anchored lo hi (<= lo at hi))
                    (and (not anchored) (or (has-suffix (first span) " (off-path)") (has-suffix (second span) " (off-path)"))))
            (return-from ptext-state "listed"))))
      (if (or (and (ptext-also-rules tx) (string= (obs-kind o) "preference"))
              (and (ptext-also-plans tx) (string= (obs-status o) +status-proposed+)))
          "listed"
          ""))))

(defun check-promise (branch at)
  "CheckPromise: (compaction lost leaked mismatch unread), or nil when the compaction carries no syndrome."
  (let ((e (and (< -1 at (length branch)) (aref branch at))))
    (when (and e (string= (entry-type e) "compaction"))
      (let ((syndrome (promise-syndrome-of (entry-details e)))
            (details (memory-details-from (entry-details e))))
        (when (and syndrome details)
          (let* ((shown (shown-set (details-shown details)))
                 (promised (promised-at branch at))
                 (mismatch (string/= (promise-digest promised shown) (jget syndrome "digest")))
                 (tx (read-promises (entry-summary e) (details-summary-codec details) (details-summary-format details))))
            (if (null tx)
                (list (entry-id e) '() '() mismatch t)
                (let ((positions (entry-index-by-id (subseq branch 0 at))) (lost '()) (leaked '()))
                  (dolist (p promised)
                    (let ((want (promise-state p shown)) (got (ptext-state tx (car p) positions)))
                      (cond ((string= want "held") (when (string= got "shown") (push-end (obs-id (car p)) leaked)))
                            ((or (and (string= want "shown") (string/= got "shown"))
                                 (and (string= want "listed") (string= got "")))
                             (push-end (obs-id (car p)) lost)))))
                  (list (entry-id e) lost leaked mismatch nil)))))))))

(defun previous-promise-index (entries)
  "PreviousPromiseIndex: the newest compaction carrying a syndrome, or -1."
  (loop for i from (1- (length entries)) downto 0
        do (let ((e (aref entries i)))
             (when (and (string= (entry-type e) "compaction") (promise-syndrome-of (entry-details e)))
               (return i)))
        finally (return -1)))

(defun compaction-promise-json (entries records all shown-ids)
  "hooks.promiseSyndromeV2's syndrome as JSON: the summary's protected records digested, with the
previous compaction's syndrome rechecked."
  (let ((prev (previous-promise-index entries)))
    (promise-syndrome-json (promised records all) shown-ids (and (>= prev 0) (check-promise entries prev)))))

(defun sleep-promise-checks (entries checked)
  "SleepPromiseChecks: the checks of the compactions no earlier sleep record checked, the newest
+max-promise-checks+, in branch order; checked is the set of compaction ids checked."
  (let ((at (loop for e across entries for i from 0
                  when (and (string= (entry-type e) "compaction") (not (gethash (entry-id e) checked))
                            (promise-syndrome-of (entry-details e)))
                    collect i)))
    (when (> (length at) +max-promise-checks+) (setf at (last at +max-promise-checks+)))
    (remove nil (mapcar (lambda (i) (check-promise entries i)) at))))

(defun promise-checks (entries)
  "PromiseChecks: every check on entries, one per compaction, in branch order, findings joined."
  (let ((out '()) (by (make-set)))
    (flet ((add (c)
             (let ((p (gethash (first c) by)))
               (if (null p)
                   (let ((copy (copy-list c))) (setf (gethash (first c) by) copy) (push-end copy out))
                   (progn
                     (dolist (id (second c)) (unless (member id (second p) :test #'string=) (setf (second p) (append (second p) (list id)))))
                     (dolist (id (third c)) (unless (member id (third p) :test #'string=) (setf (third p) (append (third p) (list id)))))
                     (setf (fourth p) (or (fourth p) (fourth c)) (fifth p) (and (fifth p) (fifth c))))))))
      (loop for e across entries
            do (if (string= (entry-type e) "compaction")
                   (let ((s (promise-syndrome-of (entry-details e))))
                     (when s (let ((c (promise-check-from (jget s "check")))) (when c (add c)))))
                   (let ((d (sleep-data e)))
                     (when d (dolist (m (sleep-items d "promises")) (let ((c (promise-check-from m))) (when c (add c)))))))))
    out))
