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

(defun stale-rec-from (m)
  (make-stale-rec :id (jget m "id") :path (jget m "path") :since (jget m "since") :head (jget m "head")
                  :blob (jget m "blob") :diff (jget m "diff") :diff-known (jtrue-p (jget m "diffKnown"))
                  :truncated (jtrue-p (jget m "truncated"))))

(deftest project-level1-cases
  ;; contracts/project-level1.jsonl: the possibly stale list Go builds and the stale verdicts it
  ;; validates, which the image must equal byte for byte.
  (let ((cases (contract-cases "project-level1.jsonl")))
    (check (length cases) 4 "cases read")
    (dolist (c cases)
      (let* ((name (jget c "name"))
             (candidates (mapcar #'observation-from (jlist-of (jget c "candidates"))))
             (active (mapcar #'observation-from (jlist-of (jget c "active"))))
             (all (append candidates active))
             (stale (mapcar #'stale-rec-from (jlist-of (jget c "stale")))))
        (multiple-value-bind (prompt ok)
            (build-prompt (make-pin* :candidates candidates :active active :available (truncate (jget c "available"))
                                     :memory-budget (truncate (jget c "memoryBudget")) :stale stale))
          (check ok t name)
          (check (prompt-text prompt) (jget c "prompt") (cat name ": prompt"))
          (check (mapcar #'stale-rec-id (prompt-stale prompt)) (jlist-of (jget c "shown")) (cat name ": shown"))
          (check (prompt-omitted-stale prompt) (truncate (jget c "omitted")) (cat name ": omitted"))
          (let* ((folded (let ((f (make-folded)))
                           (dolist (o all) (setf (gethash (obs-id o) (folded-by-id f)) o))
                           (setf (folded-observations f) all)
                           f))
                 (result (validate-level1 (jget c "args")
                                          (make-vinput :candidates candidates :active active :folded folded
                                                       :subjects (subjects-of all) :stale (prompt-stale prompt)))))
            (check (mapcar (lambda (o) (json-string (json-parse (obs-json o)))) (vresult-records result))
                   (mapcar (lambda (m) (json-string m)) (jlist-of (jget c "records"))) (cat name ": records"))
            (check (vresult-rechecked result) (truncate (jget c "rechecked")) (cat name ": rechecked"))
            (check (mapcar (lambda (r) (format nil "~A ~D ~A" (first r) (second r) (third r))) (vresult-refusals result))
                   (jlist-of (jget c "refusals")) (cat name ": refusals"))))))))

(defun head-case-view (c)
  "The copy of omhost's headcases_test fakeView: a list query paged from the case's files under
the prefix, every other query answered with the case's answer."
  (lambda (q)
    (let ((list (jget q "list")))
      (if (not (hash-table-p list))
          (jget c "answer")
          (let* ((prefix (str (jget list "prefix")))
                 (offset (let ((n (jget list "offset"))) (if (realp n) (truncate n) 0)))
                 (limit (let ((n (jget list "limit"))) (if (realp n) (truncate n) 0)))
                 (under (remove-if-not (lambda (f) (has-prefix f prefix)) (jlist-of (jget c "files"))))
                 (answer (jobject "repoId" (jget (jget c "answer") "repoId") "head" (jget (jget c "answer") "head")
                                  "total" (length under))))
            (when (eq (jget c "truncated") :true) (setf (gethash "truncated" answer) :true))
            (when (< offset (length under))
              (setf (gethash "files" answer)
                    (coerce (subseq under offset (min (length under) (+ offset limit))) 'simple-vector)))
            answer)))))

(defun head-case-lines (notes)
  (loop for n in notes
        collect (om.layer::show-string n)
        append (mapcar (lambda (w) (cat "  " (om.layer::show-string w))) (om.tool:note-with n))))

(defun head-case-read (h)
  "One om.Head read of a case: (:text Go's String :next the next page) or (:error message)."
  (let ((limit (jget h "limit")) (offset (jget h "offset")))
    (handler-case
        (let ((v (om.tool:head (str (jget h "prefix")) :limit (and limit (truncate limit))
                                                      :offset (and offset (truncate offset)))))
          ;; show prints show-string and a newline: Go's String.
          (list :text (cat (om.layer::show-string v) (string #\Newline)) :next (om.tool:head-view-next v)))
      (om.layer::tool-error (e) (list :error (om.layer::tool-error-message e))))))

(deftest project-tool-cases
  ;; contracts/project-tool.jsonl: the memory tool's notes with their freshness verdicts
  ;; (om.Note.Line) and om.Head's pages (om.HeadView.String), which the Lisp tool must print
  ;; byte for byte.
  (let ((cases (contract-cases "project-tool.jsonl")) (nexts 0))
    (check (length cases) 3 "cases read")
    (dolist (c cases)
      (let* ((name (jget c "name"))
             (branch (coerce (mapcar #'entry-from-map (jlist-of (jget c "entries"))) 'simple-vector))
             (view (and (not (eq (jget c "noView") :true)) (head-case-view c)))
             (call (om.layer:make-call
                    :branch branch :model "m"
                    :freshness (and view (lambda () (om.server::branch-freshness branch (jget c "repo") view)))
                    :structure view)))
        (om.layer:with-call (call)
          (check (head-case-lines (om.tool:notes)) (jlist-of (jget c "notes")) (cat name ": notes"))
          (when (jget c "search")
            (check (head-case-lines (om.tool:notes :search (jlist-of (jget c "search")))) (jlist-of (jget c "found"))
                   (cat name ": search")))
          (dolist (h (jlist-of (jget c "heads")))
            (let ((got (head-case-read h)))
              (if (jget h "error")
                  (check (getf got :error) (jget h "error") (cat name ": head error"))
                  (progn
                    (check (getf got :text) (jget h "text") (cat name ": head " (str (jget h "prefix"))))
                    ;; The next page's Go pointer runs as the Lisp call it stands for.
                    (when (string/= (getf got :next) "")
                      (incf nexts)
                      (let ((limit (min (truncate (or (jget h "limit") 50)) 500))
                            (offset (truncate (or (jget h "offset") 0))))
                        (check (om.layer::go-snippet-lisp (getf got :next))
                               (format nil "(head ~S :offset ~D :limit ~D)" (str (jget h "prefix")) (+ offset limit) limit)
                               (cat name ": next")))))))))))
    (check (> nexts 2) t "next pages read")))

(deftest project-tool-wire
  ;; The wire carries a note's verdict and a head page as members of their own, as om.Note.Fresh
  ;; and om.HeadView are.
  (let* ((n (om.layer::make-note :id "a" :content "c" :anchor "u1" :fresh "gone at 2222222"))
         (v (om.layer::make-head-view :repo-id "r" :head "h" :prefix "p" :files '("p/x") :offset 3 :total 9
                                      :truncated t :next "om.Head(\"p\", om.Offset(4), om.Limit(1))"))
         (n2 (om.layer::wire-decode (om.layer::wire-encode n) :keywords :intern))
         (v2 (om.layer::wire-decode (om.layer::wire-encode v) :keywords :intern)))
    (check (list (om.layer::note-content n2) (om.layer::note-fresh n2) (om.layer::go-note-line n2))
           '("c" "gone at 2222222" "c (gone at 2222222) u1") "a note's verdict on the wire")
    (check (om.layer::show-string v2) (om.layer::show-string v) "a head page on the wire")
    (check (om.layer::head-view-truncated v2) t "truncated on the wire")))
