;;;; W5: /om:view's text (memory/commands/view.go ViewOutput, NothingVisibleNote,
;;;; RenderContentOnlyProjection). It only reads; copying to a clipboard and showing it stay with
;;;; the client.

(in-package "OM")

(defun locale-int (n)
  "jsfmt.Locale: n with a comma every three digits."
  (let* ((digits (format nil "~D" (abs n)))
         (out (with-output-to-string (s)
                (loop for c across digits for i from 0
                      do (when (and (> i 0) (zerop (mod (- (length digits) i) 3))) (write-char #\, s))
                         (write-char c s)))))
    (if (minusp n) (cat "-" out) out)))

(defun view-plural (count noun)
  (if (= count 1) (cat "1 " noun) (cat (locale-int count) " " noun "s")))

(defun nothing-visible-note (entries)
  "commands.NothingVisibleNote: the note the default view prints when the agent sees no memory
although records exist; \"\" otherwise."
  (let ((visible (visible-projection entries)))
    (when (or (projection-observations visible) (projection-reflections visible))
      (return-from nothing-visible-note "")))
  (let* ((full (full-projection* entries))
         (no (length (projection-observations full))) (nr (length (projection-reflections full)))
         (parts '()))
    (when (> no 0) (push-end (view-plural no "active observation") parts))
    (when (> nr 0) (push-end (view-plural nr "reflection") parts))
    (if (null parts)
        ""
        (let ((one (= (+ no nr) 1)))
          (format nil "~A: ~A ~A recorded; /om:view full shows ~A."
                  (if (>= (find-last-compaction-index entries) 0)
                      "The latest compaction carries no observational memory"
                      "Nothing compacted yet")
                  (join parts " and ") (if one "is" "are") (if one "it" "them"))))))

(defun content-only-projection (observations reflections scope)
  (flet ((lines (items fn empty) (if items (join (mapcar fn items) (string #\Newline)) empty)))
    (join (list "── Reflections ──"
                (lines reflections #'reflection-summary-line (cat "No " scope " reflections."))
                ""
                "── Observations ──"
                (lines observations #'observation-summary-line (cat "No " scope " observations.")))
          (string #\Newline))))

;;; Per-record transparency (memory/commands/records.go): /om:view records [page] and
;;; /om:view record <id>. Read only; "used" is a view and never a rank (decision 6).

(defparameter +records-page-size+ 20)
(defparameter +records-usage+ "Usage: /om:view records [page] | /om:view record <id>")
(defparameter +max-used-listed+ 5)

(defun view-one-line (text) (join (fields (redact-secrets text)) " "))
(defun view-clip (s n) (if (<= (length s) n) s (cat (subseq s 0 n) "…")))
(defun or-word (s word) (if (string= s "") word s))
(defun short-sha (sha) (if (> (length sha) 7) (subseq sha 0 7) sha))

(defun records-page (entries arg)
  "RecordsPage."
  (let* ((f (fold-ledger entries)) (records (folded-observations f)) (n (length records)))
    (when (zerop n) (return-from records-page "No records."))
    (let ((pages (ceiling n +records-page-size+)) (page 1))
      (when (string/= arg "")
        (let ((p (and (plusp (length arg)) (every #'digit-char-p arg) (parse-integer arg))))
          (unless (and p (>= p 1)) (return-from records-page +records-usage+))
          (setf page p)))
      (when (> page pages) (return-from records-page (format nil "Page ~D is past the last page, ~D." page pages)))
      (let ((labels (folded-labels f))
            (lines (list (format nil "── Records (page ~D of ~D, ~D recorded) ──" page pages n))))
        (dolist (o (subseq records (* (1- page) +records-page-size+) (min (* page +records-page-size+) n)))
          (let ((label (gethash (obs-id o) labels "")))
            (when (gethash (obs-id o) (folded-dropped f)) (setf label (cat label ", dropped")))
            (push-end (format nil "~A ~A ~A ~A/~A: ~A" (obs-id o) (or-word (obs-kind o) "fact") label
                              (or-word (obs-source o) "unknown") (or-word (obs-authority o) "unknown")
                              (view-clip (view-one-line (obs-content o)) 100))
                      lines)))
        (push-end (if (< page pages)
                      (format nil "/om:view records ~D shows the next page; /om:view record <id> shows one record." (1+ page))
                      "/om:view record <id> shows one record.")
                  lines)
        (join lines (string #\Newline))))))

(defun writer-text (w)
  (let ((parts (loop for name in '("harness" "session" "model" "engine")
                     for v = (alist-member w name)
                     when (string/= v "") collect (cat name " " v))))
    (if parts (join parts ", ") "unknown")))

(defun frame-text (fr)
  (let ((repo (alist-member fr "repoId")))
    (if (string= repo "")
        "none"
        (cat "repo " repo
             (if (string/= (alist-member fr "branch") "") (cat " on " (alist-member fr "branch")) "")
             (if (string/= (alist-member fr "head") "") (cat " at " (short-sha (alist-member fr "head"))) "")
             (if (string/= (alist-member fr "path") "") (cat ", path " (alist-member fr "path")) "")
             (if (string/= (alist-member fr "symbol") "") (cat ", symbol " (alist-member fr "symbol")) "")))))

(defun view-strings (v) (if (and (vectorp v) (not (stringp v))) (remove-if-not #'stringp (coerce v 'list)) '()))

(defun used-by (entries id)
  "usedBy: (values shown hits recalls searches)."
  (let ((shown '()) (hits 0) (recalls 0) (searches '()))
    (loop for e across entries
          do (cond ((string= (entry-type e) "compaction")
                    (let ((d (entry-details e)))
                      (when (and (hash-table-p d) (member-str id (view-strings (jget d "shown"))))
                        (push-end (entry-id e) shown))))
                   ((string= (entry-type e) "message")
                    (let* ((m (entry-message e)) (d (and m (jget m "details"))))
                      (when (and m (equal (jget m "role") "toolResult") (equal (jget m "toolName") +memory-tool+)
                                 (hash-table-p d) (nth-value 1 (gethash "code" d)) (nth-value 1 (gethash "outcome" d)))
                        (incf recalls (count id (view-strings (jget d "recalled")) :test #'string=))
                        (when (member-str id (view-strings (jget d "hits")))
                          (incf hits)
                          (dolist (s (view-strings (jget d "searches")))
                            (let ((s (view-one-line s)))
                              (when (and (string/= s "") (< (length searches) +max-used-listed+) (not (member-str s searches)))
                                (push-end s searches))))))))))
    (values shown hits recalls searches)))

(defun used-listed (ids)
  (if (<= (length ids) +max-used-listed+)
      (join ids ", ")
      (cat (join (last ids +max-used-listed+) ", ") (format nil " (the latest ~D)" +max-used-listed+))))

(defun record-sleep-lines (st id)
  (let ((out '()))
    (let ((d (gethash id (sstate*-duplicates st))))
      (when d (push-end (format nil "duplicate of ~A (by ~A), never shown" (str (jget d "of")) (str (jget d "by"))) out)))
    (dolist (l (gethash id (sstate*-links st)))
      (push-end (format nil "shares ~A with ~A" (str (jget l "key"))
                        (join (remove id (view-strings (jget l "ids")) :test #'string=) ", "))
                out))
    (let ((v (gethash id (sstate*-verified st))))
      (when v (push-end (format nil "verified at ~A: ~A" (short-sha (str (jget v "head"))) (str (jget v "verdict"))) out)))
    (let ((r (gethash id (sstate*-retired st))))
      (when r (push-end (format nil "retired: ~A at ~A (kept)" (str (jget r "reason")) (short-sha (str (jget r "head")))) out)))
    (let ((c (gethash id (sstate*-checks st))))
      (when c
        (push-end (cat (format nil "~A check: ~A" (str (jget c "kind")) (str (jget c "verdict")))
                       (let ((by (view-strings (jget c "by")))) (if by (cat " by " (join by ", ")) "")))
                  out)))
    (when (gethash id (sstate*-decayed st)) (push-end "decay lowers its rank" out))
    out))

(defun record-view (entries id)
  "RecordView."
  (when (string= id "") (return-from record-view +records-usage+))
  (let ((f (fold-ledger entries)) (nl (string #\Newline)))
    (when (nth-value 1 (gethash id (truth-forgotten (folded-truth f))))
      (return-from record-view
        (format nil "Record ~A was forgotten: a forget record tombstoned it, so its text is not shown." id)))
    (let ((r (gethash id (folded-refl-by-id f))))
      (when r
        (return-from record-view
          (join (list (format nil "── Reflection ~A ──" id)
                      (cat "Content: " (view-one-line (refl-content r)))
                      (cat "Cites: " (or-word (join (refl-supporting r) ", ") "nothing"))
                      (cat "Standing: " (if (= (length (active-reflections (list r))) 1) "yes" "no")))
                nl))))
    (let ((o (gethash id (folded-by-id f))))
      (unless o (return-from record-view (format nil "No record ~A." id)))
      (let* ((labels (folded-labels f))
             (written (cond ((gethash id (consolidated-observation-ids entries)) "level 1 (the compaction's request)")
                            ((string= (obs-origin o) "model") "the session model (memory tool)")
                            ((string= (obs-origin o) "host") "a host summary")
                            (t "level 0 (code)")))
             (lifecycle (gethash id labels "")))
        (multiple-value-bind (by ok) (gethash id (folded-replaced-by f))
          (when ok (setf lifecycle (cat lifecycle "; replaced by " by))))
        (when (gethash id (folded-dropped f)) (setf lifecycle (cat lifecycle "; dropped from the summary")))
        (when (obs-supersedes o) (setf lifecycle (cat lifecycle "; supersedes " (join (obs-supersedes o) ", "))))
        (when (obs-conflicts o) (setf lifecycle (cat lifecycle "; conflicts with " (join (obs-conflicts o) ", "))))
        (let ((sleep (record-sleep-lines (sleep-state-of entries) id)))
          (multiple-value-bind (shown hits recalls searches) (used-by entries id)
            (join (list (format nil "── Record ~A ──" id)
                        (cat "Content: " (view-one-line (obs-content o)))
                        (format nil "Kind: ~A; status: ~A; written by ~A" (or-word (obs-kind o) "fact") (or-word (obs-status o) "stated") written)
                        (format nil "Provenance: source ~A, authority ~A, hedge ~A; writer ~A; frame ~A"
                                (or-word (obs-source o) "unknown") (or-word (obs-authority o) "unknown") (or-word (obs-hedge o) "unknown")
                                (writer-text (obs-writer o)) (frame-text (obs-frame o)))
                        (cat "Scope: " (or-word (obs-scope o) "this session"))
                        (cat "Lifecycle: " lifecycle)
                        (if sleep (cat "Sleep: " (join sleep "; ")) "Sleep: nothing recorded")
                        (cat (format nil "Used (a view, never a rank): shown in ~D summaries" (length shown))
                             (if shown (cat " (" (used-listed shown) ")") "")
                             (format nil "; returned by ~D memory tool calls, recalled by ~D" hits recalls)
                             (if searches (cat "; searches: " (join searches " | ")) ""))
                        (cat "Sources: " (or-word (join (obs-source-ids o) ", ") "none")))
                  nl)))))))

(defun view-text (entries args)
  "RunView's text for args: (values text notice), notice true for the nothing-visible note and
the usage line (shown, never copied)."
  (let* ((all (fields args)) (mode (or (first all) "")))
    (cond ((string= mode "records")
           (return-from view-text
             (if (> (length all) 2)
                 (values +records-usage+ t)
                 (let ((out (records-page entries (or (second all) "")))) (values out (string= out +records-usage+))))))
          ((string= mode "record")
           (return-from view-text
             (if (= (length all) 2) (values (record-view entries (second all)) nil) (values +records-usage+ t)))))
    (when (member mode '("" "visible") :test #'string=)
      (let ((note (nothing-visible-note entries)))
        (when (string/= note "") (return-from view-text (values note t)))))
    (multiple-value-bind (p scope)
        (cond ((string= mode "full") (values (full-projection* entries) "recorded"))
              ((member mode '("" "visible") :test #'string=) (values (visible-projection entries) "visible"))
              (t (return-from view-text (values "Usage: /om:view [full | records [page] | record <id>]" t))))
      (values (content-only-projection (projection-observations p) (active-reflections (projection-reflections p)) scope)
              nil))))
