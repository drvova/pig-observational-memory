;;;; memory/ledger render.go and render_tables.go: the bracketed record lines level 1's input
;;;; reads, and TablesSummaryBody, which finds a tables summary's encoded tables in any codec.

(in-package "OM.KERNEL")

(defun observation-summary-line (o)
  "ObservationToSummaryLine: [id] timestamp [relevance] content."
  (cat "[" (obs-id o) "] " (obs-timestamp o) " [" (obs-relevance o) "] " (obs-content o)))

(defun observation-observer-line (o)
  "ObservationToObserverLine: the summary line, then its structured fields in braces."
  (let ((fields '()) (c (make-canonical :subject (obs-subject o))))
    (unless (string= (obs-kind o) "") (push-end (cat "kind: " (obs-kind o)) fields))
    (unless (string= (canonical-subject c) "") (push-end (cat "subject: " (canonical-subject c)) fields))
    (let ((scope (canonical-scope (obs-scope o) c)))
      (unless (string= scope "") (push-end (cat "scope: " scope) fields)))
    (when (obs-supersedes o) (push-end (cat "supersedes: " (join (obs-supersedes o) " ")) fields))
    (loop for (name . value) in (obs-fields o)
          do (unless (string= value "") (push-end (cat name ": " value) fields)))
    (let ((line (observation-summary-line o)))
      (if fields (cat line " {" (join fields "; ") "}") line))))

(defun reflection-summary-line (r) (cat "[" (refl-id r) "] " (refl-content r)))

(defparameter +ledger-tables-summary-intro+ "These are condensed memories from earlier in this session, as tables")
(defparameter +ledger-omitted-notice-start+ "Shown here: ")

(defparameter +tables-recall-paragraphs+
  ;; TablesRecallParagraph, then the frozen paragraphs of the code tool's first build and of the
  ;; query language, which summaries written then end their instructions with.
  (list "The memory tool runs Go code: it reads any record back with its exact source, omitted ones included (om.Recall(`<id>`)), and searches what these tables condensed (om.History(om.Search(`<words>`))); use it when precision needs the source."
        "The memory tool runs Go code: it reads any record back with its exact source, omitted ones included (om.Recall(`id`)), and searches what these tables condensed (om.History(om.Search(`w`))); use it when precision needs the source."
        "The memory tool reads any record back with its exact source, omitted ones included (recall <id>), and searches what these tables condensed (history | search \"words\"); use it when precision needs the source."))

(defun tables-summary-body (summary)
  "TablesSummaryBody: (values body ok)."
  (unless (has-prefix summary +ledger-tables-summary-intro+) (return-from tables-summary-body (values "" nil)))
  (let ((body nil))
    (dolist (p +tables-recall-paragraphs+)
      (multiple-value-bind (before after found) (cut summary p)
        (declare (ignore before))
        (when found (setf body after) (return))))
    (unless body (return-from tables-summary-body (values "" nil)))
    (setf body (values (cut body (format nil "~%~%---~%~%"))))
    (let ((trimmed (trim-space body)))
      (when (has-prefix trimmed +ledger-omitted-notice-start+)
        (setf body (nth-value 1 (cut (subseq trimmed (length +ledger-omitted-notice-start+)) (format nil "~%~%"))))))
    (values (trim-space body) t)))
