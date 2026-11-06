;;;; The memory tool's call as the image serves it (docs/lisp-port-tool-layer.md, "The memory tool
;;;; in a child"): harness.Manager.RunMemory's shape over the Lisp tool. The code runs in a child
;;;; (child.lisp); the answer is the text, whether it is an error, and omtool.Details as JSON, in
;;;; Go's member order, so level 0 reads the writes back (extract's notes of saves and edits) and
;;;; the detectors the recalls and searches; one ModelKindCode event goes to the session model's
;;;; usage ledger, classified against what the summary and the index show (recordCode).

(in-package "OM.LAYER")

;;; The manual

(defparameter +lisp-manual+
  "The memory tool runs Common Lisp over this session's memory.

Each call's forms are read by a checked reader (no eval, files, streams, packages or other
symbols outside the allowed set) and run in a child process under a time limit. The tool shows
what the code printed; only when nothing was printed does it show the last form's value.
Functions you define with defun last for the call only.

── Reads ──
(notes &key search kind status subject since until from through limit)
  The active records, oldest first. search: a string or a list of strings, every word must
  appear (case-insensitive, over every field); when no record holds every word, the records
  holding any are ranked instead. since/until: YYYY-MM-DD. from/through: an entry or record id
  (branch position). limit: 1 to 200, 20 by default.
(note-by-id id)                 One record by id, dropped ones included.
(history &key search limit)     User, assistant and tool text, newest first, without the current turn.
(recall id &key offset)         A record's sources, else an entry, else a saved memory by name, paged.
(saved &key search)             The curated memories this project and session see, ranked.
(text name)                     A text from the call's strings argument.

── Records ──
note-id note-when note-kind note-subject note-scope note-relevance note-content note-status
note-event-date note-origin note-outcome note-tool note-target note-source note-anchor
note-superseded-by (the ids of the records that replaced it) note-line (the record as one line)
turn-id turn-when turn-role turn-text
memory-name memory-type memory-description memory-body

── Curated memory ──
(save name type description &key body scope quoting)
  Saves a memory, staged unless quoting gives the user's confirming words. type: user, feedback,
  project, reference or lesson.
(edit name &key type description body)   (feedback name outcome quote)
(forget-memory name &key quoting)
  Forgets a memory for every session when quoting gives the user's words asking for it;
  without them it hides the memory in this session only and stages the forget.

── Your layer ──
(adaptables)                    The functions a definition may replace.
(define name (params) body...)  A top-level form: holds a definition; it runs beside the default
                                at the next compactions and replaces it once the evidence settles it.
(layer)  (layer-forget name)  (layer-rollback generation)

── Output ──
(show x) prints x as the tool shows a value; format, princ and print print as usual.

A summary's om.Notes(...), om.Recall(...), om.History(...) and om.Saved(...) pointers run as
they are printed: the tool runs the Lisp call each stands for and names it.

── Go definitions ──
")

(defun code-store-units (dir)
  "The names of the Go definitions in a project's code store (codestore's units.json), in name
order: listed, never run (docs/lisp-core.md, decision 4)."
  (let ((text (and dir (read-file (join-path dir "units.json")))))
    (when text
      (let ((v (handler-case (om::json-parse (coerce text 'simple-string)) (error () nil))))
        (when (vectorp v)
          (sort (remove-duplicates (loop for u across v
                                         for name = (om::jget u "name")
                                         when (and (stringp name) (string/= name "")) collect name)
                                   :test #'string=)
                #'string<))))))

(defun code-store-dir (config-home project)
  "codestore.Dir(configHome, projectRoot): <home>/observational-memory/memories/code/<key>."
  (when (and config-home (string/= config-home "") project)
    (join-path config-home "observational-memory" "memories" "code"
               (subseq (om::sha256-octets (om::utf8-octets (string-right-trim "/" project))) 0 16))))

(defun lisp-manual (code-store)
  (let ((units (code-store-units code-store)))
    (concatenate 'string +lisp-manual+
                 (if units
                     (format nil "Kept by the Go memory tool, never run here: ~{~A~^, ~}" units)
                     "none kept by the Go memory tool"))))

;;; Details (omtool.Details, Go's member order)

(defun details-json (code strings rec outcome &key manual)
  (with-output-to-string (out)
    (flet ((key (k) (write-char #\, out) (om::write-json-string k out) (write-char #\: out))
           (strs (list)
             (write-char #\[ out)
             (loop for (s . more) on list do (om::write-json-string s out) (when more (write-char #\, out)))
             (write-char #\] out)))
      (write-string "{\"code\":" out)
      (om::write-json-string code out)
      (when strings (key "strings") (strs strings))
      (when (rec-writes rec)
        (key "writes")
        (write-char #\[ out)
        (loop for ((op name type description) . more) on (rec-writes rec)
              do (write-string "{\"op\":" out) (om::write-json-string op out)
                 (write-string ",\"name\":" out) (om::write-json-string (or name "") out)
                 (when (and (stringp type) (string/= type "")) (write-string ",\"type\":" out) (om::write-json-string type out))
                 (when (and (stringp description) (string/= description ""))
                   (write-string ",\"description\":" out) (om::write-json-string description out))
                 (write-char #\} out)
                 (when more (write-char #\, out)))
        (write-char #\] out))
      (when (rec-hits rec) (key "hits") (strs (rec-hits rec)))
      (when (rec-recalled rec) (key "recalled") (strs (rec-recalled rec)))
      (when (rec-searches rec) (key "searches") (strs (rec-searches rec)))
      (when (rec-empty-p rec) (write-string ",\"empty\":true" out))
      (key "outcome") (om::write-json-string outcome out)
      (when manual (write-string ",\"manual\":true" out))
      (write-char #\} out))))

;;; The usage event (harness recordCode, VisibilityOf, Classify)

(defun visibility (entries index-shown)
  "harness.VisibilityOf: (values shown omitted index), sets of ids and names. Memory v2's
summaries record what they show (details.shown); an older summary counts an id as shown when it
appears in the summary text (Go also leaves out the ids that start an omitted span's query)."
  (let ((shown (make-hash-table :test 'equal)) (omitted (make-hash-table :test 'equal))
        (index (make-hash-table :test 'equal)))
    (dolist (n index-shown) (setf (gethash n index) t))
    (let ((i (om::find-last-compaction-index entries)))
      (when (>= i 0)
        (let* ((e (aref entries i)) (details (om::entry-details e)) (summary (om::entry-summary e))
               (ids (append (map 'list (lambda (r) (om::jget r "id")) (let ((v (om::jget details "reflections"))) (if (vectorp v) v #())))
                            (map 'list (lambda (o) (om::jget o "id")) (let ((v (om::jget details "observations"))) (if (vectorp v) v #()))))))
          (when (and (hash-table-p details) (stringp summary) (string/= summary ""))
            (multiple-value-bind (listed present) (gethash "shown" details)
              (let ((on (make-hash-table :test 'equal)))
                (when (and present (vectorp listed))
                  (loop for id across listed when (stringp id) do (setf (gethash id on) t)))
                (dolist (id ids)
                  (when (stringp id)
                    (if (if (and present (vectorp listed)) (gethash id on) (search id summary))
                        (setf (gethash id shown) t)
                        (setf (gethash id omitted) t))))))))))
    (values shown omitted index)))

(defun classify-hits (hits shown omitted index)
  "Visibility.Classify: (values outcome target)."
  (cond ((null hits) (values "ok" nil))
        ((some (lambda (h) (gethash h omitted)) hits) (values "omitted" "summary"))
        (t (let ((in-summary nil))
             (dolist (h hits)
               (cond ((gethash h shown) (setf in-summary t))
                     ((gethash h index))
                     (t (return-from classify-hits (values "ok" nil)))))
             (if in-summary (values "shown" "summary") (values "shown" "index"))))))

(defun record-code-event (call entries outcome rec index-shown)
  "harness.Manager.recordCode: one ModelKindCode event in the session model's usage ledger
(<store>/models/<model>.jsonl, under the store's lock), when the call has a store and a model
and was not cancelled."
  (let ((dir (call-curated-dir call)) (model (call-model call)))
    (when (and dir (string/= model "") (string/= outcome "cancelled"))
      (multiple-value-bind (o target)
          (cond ((string/= outcome "ok") (values "error" nil))
                ((rec-writes rec) (values "ok" nil))
                ((rec-empty-p rec) (values "empty" nil))
                (t (multiple-value-bind (shown omitted index) (visibility entries index-shown)
                     (multiple-value-bind (o target) (classify-hits (rec-hits rec) shown omitted index)
                       (if (and (rec-recalled rec) (string= o "shown")) (values "ok" nil) (values o target))))))
        (let ((line (with-output-to-string (out)
                      (write-string "{\"time\":" out) (om::write-json-string (format-rfc3339-nano (now)) out)
                      (write-string ",\"model\":" out) (om::write-json-string model out)
                      (write-string ",\"kind\":\"code\",\"outcome\":" out) (om::write-json-string o out)
                      (when target (write-string ",\"target\":" out) (om::write-json-string target out))
                      (write-string ",\"engine\":\"v2\"}" out))))
          (handler-case
              (with-store-lock (dir)
                (append-line (join-path dir "models" (concatenate 'string (model-dir model) ".jsonl")) line))
            (error (e) (ignore-errors (format *error-output* "~&om.layer: the code event: ~A~%" e)))))))))

;;; The call

(defun strings-arg (v)
  "stringsArg: (values a hash of names to strings, or nil, and an error text)."
  (cond ((or (null v) (eq v :null)) (values nil nil))
        ((and (hash-table-p v) (loop for x being the hash-values of v always (stringp x))) (values v nil))
        (t (values nil "strings must be an object of names to text values"))))

(defun memory-tool-run (params &key (call *call*) index-shown code-store (cancelled-p (lambda () nil)))
  "harness.Manager.RunMemory over the Lisp tool: (values text is-error details-json). params
are the tool's arguments (code, strings; a call written before the code tool carries query)."
  (let* ((code-v (om::jget params "code"))
         (code (cond ((stringp code-v) code-v)
                     ((stringp (om::jget params "query")) (om::jget params "query"))
                     (t "")))
         (rec (make-tool-record)))
    (multiple-value-bind (strings err) (strings-arg (om::jget params "strings"))
      (let ((names (and strings (sort (loop for k being the hash-keys of strings collect k) #'string<))))
        (when err
          (return-from memory-tool-run
            (values (concatenate 'string "Error: " err) t (details-json code names rec "compile"))))
        (when (string= (om::trim-space code) "")
          (record-code-event call (call-branch call) "ok" rec index-shown)
          (return-from memory-tool-run
            (values (lisp-manual code-store) nil (details-json code names rec "ok" :manual t))))
        (let* ((r (run-tool-child code :call call :strings strings :cancelled-p cancelled-p))
               (status (cr-status r)) (rec (cr-record r))
               (text ""))
          (flet ((line (x)
                   ;; codeText's addLine: a line after a newline, unless the text so far ends one
                   (when (and x (string/= x ""))
                     (setf text (if (or (string= text "") (om::has-suffix text (string #\Newline)))
                                    (concatenate 'string text x)
                                    (concatenate 'string text (string #\Newline) x))))))
            (line (cr-text r))
            (cond ((member status '("compile" "runtime") :test #'string=)
                   (line (concatenate 'string "Error: " (or (cr-error r) "the code failed"))))
                  ((member status '("crashed" "cancelled") :test #'string=)
                   (line (format nil "~A: ~A" status (or (cr-error r) "")))
                   (when (rec-writes rec)
                     (line (format nil "Writes that landed before the ~A: ~{~A~^, ~}"
                                   (if (string= status "crashed") "crash" "cancel")
                                   (mapcar (lambda (w) (format nil "~A ~A" (first w) (second w))) (rec-writes rec))))))))
          (record-code-event call (call-branch call) status rec index-shown)
          (values text (not (string= status "ok")) (details-json code names rec status)))))))
