;;;; The ledger cases of the parity harness (memory/selection/lispparity_ledger_test.go): a
;;;; branch of session entries that already holds level 1's and v1's records, read as entries,
;;;; extracted once at the end, then printed as the fold, the projections, the subjects, the
;;;; summary in every format and codec, and the omitted spans of a partial listing.

(in-package "OM.ENGINE")

(defun read-entry-file (path)
  "A JSON-lines file of session entries, one decoded entry per line."
  (with-open-file (in path :external-format :utf-8)
    (coerce (loop for line = (read-line in nil)
                  while line
                  unless (string= (trim-space line) "")
                    collect (entry-from-map (json-parse line)))
            'simple-vector)))

(defun json-strings (list) (json-string (coerce list 'simple-vector)))

(defun sorted-keys (table) (sort (loop for k being the hash-keys of table collect k) #'string<))

(defun summary-lines (variant budget r out)
  (format out "summary ~A ~D ~A~%" variant budget (json-string (summary-text r)))
  (format out "stats ~A ~D ~A~%" variant budget
          (json-string (jobject "shown" (coerce (mapcar #'obs-id (summary-observations r)) 'simple-vector)
                                "reflections" (coerce (mapcar #'refl-id (summary-reflections r)) 'simple-vector)
                                "request" (jbool (summary-request r))
                                "omitted" (summary-omitted r) "spans" (summary-spans r)
                                "folded" (summary-folded r)
                                "snippets" (coerce (summary-snippets r) 'simple-vector)))))

(defun run-ledger-pipeline (path cwd budgets out)
  (let ((branch (read-entry-file path)))
    (flet ((alias-line (prefix a)
             (format nil "~A ~A ~A ~A" prefix (json-string (subject-alias-from a)) (json-string (subject-alias-to a))
                     (json-string (subject-alias-reason a)))))
      (format out "start ~D~%" (extraction-start-index branch))
      (format out "foldboundary ~A~%" (json-string (latest-full-fold-boundary-id branch)))
      (multiple-value-bind (batches covers)
          (extract branch (extraction-start-index branch) (length branch) (fold-ledger branch) :cwd cwd)
        (setf branch (append-extraction branch batches covers 0)))
      (let ((folded (fold-ledger branch)))
        (dolist (o (folded-observations folded)) (format out "folded ~A~%" (obs-json o)))
        (format out "active ~A~%" (json-strings (mapcar #'obs-id (folded-active folded))))
        (format out "dropped ~A~%" (json-strings (sorted-keys (folded-dropped folded))))
        (format out "replaced ~A~%"
                (json-strings (sort (loop for id being the hash-keys of (folded-replaced-by folded) using (hash-value by)
                                          collect (cat id ">" by))
                                    #'string<)))
        (format out "citedby ~A~%"
                (json-strings (sort (loop for id being the hash-keys of (folded-cited-by folded) using (hash-value by)
                                          collect (cat id ":" (join by ",")))
                                    #'string<)))
        (dolist (r (folded-reflections folded)) (format out "foldedreflection ~A~%" (refl-json r)))
        (dolist (a (folded-aliases folded)) (format out "~A~%" (alias-line "foldedalias" a)))
        (let* ((projection (full-projection* branch))
               (observations (projection-observations projection))
               (reflections (projection-reflections projection))
               (aliases (projection-aliases projection))
               (subjects (subjects-of observations))
               (canonical (canonical-subjects subjects aliases)))
          (dolist (o observations) (format out "record ~A~%" (obs-json o)))
          (dolist (r reflections) (format out "reflection ~A~%" (refl-json r)))
          (dolist (r (active-reflections reflections)) (format out "activereflection ~A~%" (refl-id r)))
          (dolist (a aliases) (format out "~A~%" (alias-line "alias" a)))
          (dolist (s (sorted-keys canonical))
            (let ((c (gethash s canonical)))
              (format out "canonical ~A ~A ~A ~A~%" (json-string s) (json-string (canonical-subject c))
                      (json-string (canonical-extra-scope c)) (json-string (subject-table-name (canonical-subject c))))))
          (multiple-value-bind (in-force refused) (check-subject-aliases subjects (folded-aliases folded))
            (dolist (a in-force) (format out "~A~%" (alias-line "inforce" a)))
            (dolist (r refused) (format out "~A ~A~%" (alias-line "refused" (car r)) (json-string (cdr r)))))
          (loop for e across branch
                do (when (and (string= (entry-type e) "message") (equal (entry-role e) "user"))
                     (format out "names ~A ~A~%" (entry-id e)
                             (json-string (subject-in-text (text-blocks (jget (entry-message e) "content")) subjects aliases)))))
          (let ((dates (reflection-date-ranges (folded-reflections folded) (folded-by-id folded)))
                (intent (pipeline-intent branch))
                (request (let* ((users (remove-if-not (lambda (e) (and (string= (entry-type e) "message") (equal (entry-role e) "user")))
                                                      (coerce branch 'list)))
                                (middle (nth (floor (length users) 2) users)))
                           (cons (entry-id middle) (text-blocks (jget (entry-message middle) "content"))))))
            (dolist (v '(("rows" "tables" :rows) ("jsonl" "tables" :jsonl) ("lines" "lines" :rows)))
              (destructuring-bind (name format codec) v
                (dolist (budget budgets)
                  (summary-lines name budget
                                 (render-summary observations folded branch budget :reflections reflections :request request :intent intent
                                                                                    :format format :codec codec :aliases aliases
                                                                                    :reflection-dates dates)
                                 out)))))
          (let ((listed (make-set)))
            (loop for o in observations for i from 0 do (when (zerop (mod i 3)) (setf (gethash (obs-id o) listed) t)))
            (loop for r in reflections for i from 0 do (when (zerop (mod i 3)) (setf (gethash (refl-id r) listed) t)))
            (dolist (room '(0 40 150 600 5000))
              (multiple-value-bind (spans need ok) (omitted-spans observations reflections listed folded branch room)
                (format out "spans ~D ~D ~A ~D ~A ~A~%" room need (if ok "true" "false") (spans-result-records spans)
                        (json-strings (spans-result-lines spans)) (json-strings (spans-result-snippets spans))))))
          (let ((visible (visible-projection branch)))
            (format out "visible ~D ~D~%" (length (projection-observations visible)) (length (projection-reflections visible))))
          (dolist (at (list (floor (length branch) 3) (floor (length branch) 2)))
            (let ((id (entry-id (aref branch at))))
              (multiple-value-bind (p full-fold) (build-compaction-projection branch id 60)
                (format out "compaction ~A ~A ~A ~A~%" id (if full-fold "true" "false")
                        (json-strings (mapcar #'obs-id (projection-observations p)))
                        (json-strings (mapcar #'refl-id (projection-reflections p))))))))))))
