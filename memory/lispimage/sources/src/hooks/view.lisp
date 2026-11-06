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

(defun view-text (entries args)
  "RunView's text for args: (values text notice), notice true for the nothing-visible note and
the usage line (shown, never copied)."
  (let ((mode (or (first (fields args)) "")))
    (when (member mode '("" "visible") :test #'string=)
      (let ((note (nothing-visible-note entries)))
        (when (string/= note "") (return-from view-text (values note t)))))
    (multiple-value-bind (p scope)
        (cond ((string= mode "full") (values (full-projection* entries) "recorded"))
              ((member mode '("" "visible") :test #'string=) (values (visible-projection entries) "visible"))
              (t (return-from view-text (values "Usage: /om:view [full]" t))))
      (values (content-only-projection (projection-observations p) (active-reflections (projection-reflections p)) scope)
              nil))))
