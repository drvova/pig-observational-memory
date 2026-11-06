;;;; The read-and-record path the parity harness runs on both engines
;;;; (memory/selection/lispparity_test.go): read a transcript, extract level 0 at every turn end
;;;; (or once over the whole branch), append the batches as the hooks do (appendExtraction), fold
;;;; the ledger and render the summary at each budget. The output is one line per item:
;;;;   entry <the entry's decoded object as JSON>
;;;;   record <a folded record as JSON>
;;;;   summary <budget> <the summary text as a JSON string>
;;;;   stats <budget> <what the summary shows and leaves out, as JSON>

(in-package "OM.ENGINE")

(defun custom-entry (id custom-type data)
  (make-entry :type "custom" :id id :custom-type custom-type :data data))

(defun append-extraction (branch batches covers run)
  "appendExtraction: one om.observations.recorded entry per batch, then an om.extracted marker
when the last batch stops short of what the extraction read. Ids are the harness's."
  (let ((n 0) (out '()))
    (flet ((next-id () (prog1 (format nil "om-x~D-~D" run n) (incf n))))
      (dolist (b batches)
        (when (and (batch-observations b) (string/= (batch-covers b) ""))
          (push (custom-entry (next-id) "om.observations.recorded"
                              (jobject "observations" (coerce (mapcar #'obs-to-value (batch-observations b)) 'simple-vector)
                                       "coversUpToId" (batch-covers b)))
                out)))
      (when (and (string/= covers "") (or (null batches) (string/= (batch-covers (car (last batches))) covers)))
        (push (custom-entry (next-id) "om.extracted" (jobject "coversUpToId" covers)) out)))
    (concatenate 'simple-vector branch (nreverse out))))

(defun user-text (e)
  "The harness's intent rule: a user message's text without markup, \"\" for any other entry."
  (if (and (string= (entry-type e) "message") (equal (entry-role e) "user"))
      (without-markup (text-blocks (jget (entry-message e) "content")))
      ""))

(defun pipeline-intent (branch)
  (let ((first (find-if (lambda (e) (string/= (user-text e) "")) branch))
        (latest (find-if (lambda (e) (string/= (user-text e) "")) branch :from-end t))
        (out '()))
    (when first (push-end (list "first" (entry-id first) (user-text first)) out))
    (when (and latest (not (eq latest first))) (push-end (list "latest" (entry-id latest) (user-text latest)) out))
    out))

(defun run-pipeline (format path cwd mode budgets out)
  "mode is turns or whole, optionally followed by :lines or :jsonl for the summary's format or
codec (rows tables by default)."
  (multiple-value-bind (mode variant) (cut mode ":")
  (let* ((entries (coerce (read-transcript format path) 'simple-vector))
         (branch (vector)) (run 0)
         (summary-format (if (string= variant "lines") "lines" "tables"))
         (codec (if (string= variant "jsonl") :jsonl :rows)))
    (loop for e across entries
          do (format out "entry ~A~%" (json-string (entry-to-map e))))
    (flet ((extract-now ()
             (let ((start (extraction-start-index branch)))
               (multiple-value-bind (batches covers) (extract branch start (length branch) (fold-ledger branch) :cwd cwd)
                 (setf branch (append-extraction branch batches covers run))
                 (incf run)))))
      (loop for e across entries
            do (when (and (string= mode "turns") (plusp (length branch))
                          (string= (entry-type e) "message") (equal (entry-role e) "user"))
                 (extract-now))
               (setf branch (concatenate 'simple-vector branch (vector e))))
      (extract-now))
    (let ((records (full-projection branch)) (folded (fold-ledger branch)) (intent (pipeline-intent branch)))
      (dolist (o records) (format out "record ~A~%" (obs-json o)))
      (dolist (budget budgets)
        (let ((r (render-summary records folded branch budget :intent intent :format summary-format :codec codec)))
          (format out "summary ~D ~A~%" budget (json-string (summary-text r)))
          (format out "stats ~D ~A~%" budget
                  (json-string (jobject "shown" (coerce (mapcar #'obs-id (summary-observations r)) 'simple-vector)
                                        "omitted" (summary-omitted r) "spans" (summary-spans r)
                                        "folded" (summary-folded r)
                                        "snippets" (coerce (summary-snippets r) 'simple-vector))))))))))

(defun main ()
  "sbcl --script lisp/parity.lisp <format> <transcript> <cwd> <whole|turns> <budget>..."
  (destructuring-bind (format path cwd mode &rest budgets) (rest sb-ext:*posix-argv*)
    (if (string= format "ledger")
        (run-ledger-pipeline path (if (string= cwd "-") "" cwd) (mapcar #'parse-integer budgets) *standard-output*)
        (run-pipeline format path (if (string= cwd "-") "" cwd) mode (mapcar #'parse-integer budgets) *standard-output*))
    (finish-output)))
