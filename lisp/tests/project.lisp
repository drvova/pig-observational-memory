;;;; Project records and freshness verdicts against their contract cases (contracts/project.jsonl,
;;;; Go's memory/project output, which the port must equal).

(in-package "OM")

(deftest project-contract-cases
  (let ((cases (contract-cases "project.jsonl")) (verdicts 0))
    (check (> (length cases) 15) t "cases read")
    (dolist (c cases)
      (let ((name (jget c "name")))
        (if (stringp (jget c "text"))
            (check (project-symbol-in (jget c "text")) (str (jget c "symbol")) name)
            (let ((records (mapcar #'observation-from (jlist-of (jget c "records"))))
                  (repo (jget c "repo")) (answer (jget c "answer")))
              (check (json-string (project-query records repo)) (json-string (jget c "query")) (cat name ": query"))
              (let ((got (project-verdicts records repo answer)))
                (incf verdicts (length got))
                (check (mapcar (lambda (v) (list (freshness-id v) (freshness-kind v) (freshness-text v))) got)
                       (mapcar (lambda (v) (list (jget v "id") (jget v "kind") (jget v "text"))) (jlist-of (jget c "verdicts")))
                       (cat name ": verdicts"))
                (let ((diffs (project-diff-query got)))
                  (check (if (project-query-empty-p diffs) "null" (json-string diffs))
                         (if (jget c "diffs") (json-string (jget c "diffs")) "null") (cat name ": diffs"))))
              (let ((stamped (if (project-stamp records answer)
                                 (loop for o in records
                                       when (string/= (frame-of o "bodyHash") "")
                                         collect (list (obs-id o) (frame-of o "bodyHash")))
                                 '()))
                    (want (let ((m (jget c "stamped")))
                            (when m (sort (loop for k being the hash-keys of m collect (list k (gethash k m)))
                                          #'string< :key #'first)))))
                (check stamped want (cat name ": stamped")))))))
    (check (> verdicts 3) t "verdicts given")))

(deftest project-symbol-in-frame
  (let ((*encoding* (list :frame (make-record-frame :repo-id "r1" :head "h1") :root "/w")))
    (let ((o (encode-record (make-obs :id "d1" :kind "decision" :content "we decided to keep `parseConfig` pure"
                                      :source-ids '("u1"))
                            "user-said")))
      (check (frame-of o "symbol") "parseConfig"))
    (let ((o (encode-record (make-obs :id "a1" :kind "fact" :content "`parseConfig` is pure" :source-ids '("a1"))
                            "assistant-claim")))
      (check (frame-of o "symbol") "" "an assistant's fact is no project record"))))

(deftest project-render-cases
  ;; contracts/project-render.jsonl: level 0 framed at the first commit, the verdicts against the
  ;; view's answer, and the summary Go renders with them.
  (let ((cases (contract-cases "project-render.jsonl")))
    (check (length cases) 2 "cases read")
    (dolist (c cases)
      (let* ((branch (apply #'branch-of (jlist-of (jget c "lines"))))
             (repo (jget c "repo"))
             (*encoding* (list :frame (make-record-frame :repo-id (str (jget repo "repoId"))
                                                         :head "1111111111111111111111111111111111111111")
                               :root (str (jget repo "root"))))
             (records (extract-all branch))
             (verdicts (project-verdicts records repo (jget c "answer"))))
        (check (length verdicts) 2 (jget c "name"))
        (check (summary-text (render-summary records (fold-ledger branch) branch (jget c "budget")
                                             :format (or (jget c "format") "tables")
                                             :freshness (project-texts verdicts)))
               (jget c "summary") (jget c "name"))))))
