;;;; memory/selection: the level-0 summary (selection.go, layout.go, omit.go) in the tables and
;;;; lines formats, with reflections, subject tables and OmittedSpans; the codecs it encodes
;;;; tables in (tables/rows_encode.go, tables/jsonl.go); and the om snippet formatter it prints
;;;; (om/snippet.go FormatRead). Not ported: a codec a model declared (tables/declared.go).

(in-package "OM.ENGINE")

(defparameter +default-limit+ 20)
(defparameter +max-limit+ 200)
(defparameter +history-clip-chars+ 400)
(defparameter +off-path-label+ " (off-path)")

;;; Fixed texts

(defparameter +snippet-recall+ "om.Recall(`<id>`)")
(defparameter +snippet-recall-entry+ "om.Recall(`<entry id>`)")
(defparameter +snippet-history-search+ "om.History(om.Search(`<words>`))")
(defparameter +tables-summary-intro+ "These are condensed memories from earlier in this session, as tables")
(defparameter +tables-recall-paragraph+
  (cat "The memory tool runs Go code: it reads any record back with its exact source, omitted ones included ("
       +snippet-recall+ "), and searches what these tables condensed (" +snippet-history-search+
       "); use it when precision needs the source."))
(defparameter +rows-format-line+
  ": name[rows]{fields}: then one record per line, values in field order; an empty value means none.")

(defun nl (&rest lines) (join lines (string #\Newline)))

(defparameter +tables-body+
  (cat (nl ""
           ""
           "- request: the opening request of the turn this compaction cut through; the turn continues after this summary."
           "- reflections: stable facts about the user, project, decisions, and constraints; dates is when their records were made."
           "- notes.<subject>: records about one subject, oldest first; notes: records without one. kind is fact, event, preference, plan, correction, assistant (something the assistant produced), attempt (a tool call and its outcome: worked, failed or open) or note (one written with the memory tool); status is proposed (planned, not done), hypothetical or retracted; superseded_by is the id of the record that replaced this one; content may end with (was <id>), the record it replaced, or (may update <id>) and (conflicts <id>), an earlier record it may replace or contradict."
           (cat "- source is the entry id a record was picked from; the memory tool's " +snippet-recall-entry+ " reads that entry.")
           "- omitted lists records left out to fit: date range, count, how many spans a row folds together, first and last entry id, and in query the om snippet (memory tool code) that lists them."
           "- The files and commits lines after the tables list files at their newest state and commits, each with its entry id; +N more, all M ends a list with the om snippets that list the whole list."
           ""
           "Treat these as past records. When records conflict, the most recent reflects the latest known state. Work described as completed should not be redone unless the user explicitly asks."
           ""
           "")
       +tables-recall-paragraph+))

(defparameter +tables-instructions+ (cat +tables-summary-intro+ +rows-format-line+ +tables-body+))

(defparameter +lines-instructions+
  (cat (nl "These are condensed memories from earlier in this session, one record per line under a heading."
           ""
           "- request: the opening request of the turn this compaction cut through; the turn continues after this summary."
           "- reflections: stable facts about the user, project, decisions, and constraints, as [id] (dates) content."
           "- notes.<subject> and notes: records as [id] date kind status content, oldest first. kind is fact, event, preference, plan, correction, assistant, attempt (a tool call and its outcome) or note (one written with the memory tool); status is proposed (planned, not done), hypothetical or retracted. A line may end with (was <id>), the record it replaced, or (may update <id>) and (conflicts <id>), an earlier record it may replace or contradict."
           (cat "- Every line ends with the entry id it was picked from, in parentheses; the memory tool's " +snippet-recall-entry+ " reads that entry.")
           "- omitted lists records left out to fit, each span with its dates, count, first and last entry id and the om snippet (memory tool code) that lists them."
           "- files and commits list files at their newest state and commits; +N more, all M ends a list with the om snippets that list the whole list."
           ""
           "Treat these as past records. When records conflict, the most recent reflects the latest known state. Work described as completed should not be redone unless the user explicitly asks."
           ""
           "")
       +tables-recall-paragraph+))

(defparameter +request-line+
  (cat "- request: the opening request of the turn this compaction cut through; the turn continues after this summary." (string #\Newline)))
(defparameter +intent-line+
  (cat "- intent: what this session is for, in the user's words: first is the session's opening request, latest the latest request this summary covers; a cut one ends with … and its source reads it whole." (string #\Newline)))
(defparameter +next-line+
  (cat "- next: the next steps of the current work: plans still standing (status proposed) and attempts still open or last failed, made since the latest request; they are not repeated in the notes tables." (string #\Newline)))
(defparameter +next-lines-format+
  (cat "- next: the next steps of the current work, as notes lines: plans still standing (status proposed) and attempts still open or last failed, made since the latest request; they are not repeated under notes." (string #\Newline)))
(defparameter +trailer-separator+ (format nil "~%~%---~%~%"))
;; The instructions lines a kind adds once to a summary that shows one of its records (an item's
;; extra, a key no group has): evidence rows, and the values lists after the tables.
(defparameter +extra-evidence+ "line:evidence")
(defparameter +extra-values+ "line:values")
(defparameter +evidence-line+
  (cat "- evidence: a notes row of kind evidence holds lines a tool printed when its call failed or reported an error, verbatim and joined by ⏎; its content names the call." (string #\Newline)))
(defparameter +values-line+
  (cat "- values.<kind> (after ---): exact values tool output printed (path, error type, sha, url, kv for KEY=value, ip, issue, failing test, branch, searched symbol, version), the most often printed first; ×N counts the results that printed it, and its entry id is the first that did; +N more, all M ends with the om snippets that list every value." (string #\Newline)))
(defparameter +max-values-per-kind+ 40)
(defparameter +omitted-advice+
  (cat "Something missing from this summary may still be in memory: before saying it was not mentioned or is unknown, run the om snippet that lists what was left out or "
       +snippet-history-search+ "."))
(defparameter +omitted-notice-start+ "Shown here: ")

(defparameter +request-fields+ '("content" "source"))
(defparameter +intent-fields+ '("which" "content" "source"))
(defparameter +reflection-fields+ '("id" "dates" "content" "source"))
(defparameter +note-fields+ '("id" "when" "kind" "status" "scope" "content" "superseded_by" "source"))
(defparameter +omitted-fields+ '("from" "to" "count" "spans" "first_id" "last_id" "query"))

;;; Codecs: the rows codec (rows_encode.go) and JSON lines (jsonl.go). A codec is :rows or :jsonl.

(defun needs-quote-p (s)
  (or (string= s "")
      (space-p (char s 0))
      (space-p (char s (1- (length s))))
      (contains-any s (format nil ",:\"\\|~C~C" #\Newline #\Return))))

(defun quote-value (s)
  (if (not (needs-quote-p s))
      s
      (with-output-to-string (out)
        (write-char #\" out)
        (loop for c across s
              do (case c
                   (#\" (write-string "\\\"" out))
                   (#\\ (write-string "\\\\" out))
                   (#\Newline (write-string "\\n" out))
                   (#\Return (write-string "\\r" out))
                   (#\Tab (write-string "\\t" out))
                   (t (write-char c out))))
        (write-char #\" out))))

(defstruct rtable name fields (rows '()) (absent '()))

(defun cell-absent-p (absent c) (nth c absent))

(defun encode-table (tb)
  "The rows codec's encoding of one table."
  (with-output-to-string (out)
    (format out "~A[~D]{~A}:" (rtable-name tb) (length (rtable-rows tb)) (join (rtable-fields tb) ","))
    (loop for row in (rtable-rows tb) for i from 0
          for absent = (nth i (rtable-absent tb))
          do (write-string (format nil "~%  ") out)
             (loop for c from 0 below (length (rtable-fields tb))
                   do (when (> c 0) (write-char #\, out))
                      (cond ((and (< c (length row)) (not (cell-absent-p absent c))) (write-string (quote-value (nth c row)) out))
                            ((= (length (rtable-fields tb)) 1) (write-string "\"\"" out)))))))

(defun encode-jsonl-table (tb)
  "The JSON-lines codec's encoding of one table: a header line, then one array per row."
  (let ((lines (list (cat "{\"table\":" (json-string (rtable-name tb) :escape-html nil)
                          ",\"fields\":" (json-string (coerce (rtable-fields tb) 'simple-vector) :escape-html nil) "}"))))
    (loop for row in (rtable-rows tb) for i from 0
          for absent = (nth i (rtable-absent tb))
          do (push-end (json-string (coerce (loop for c from 0 below (length (rtable-fields tb))
                                                  collect (if (and (< c (length row)) (not (cell-absent-p absent c)))
                                                              (nth c row)
                                                              :null))
                                            'simple-vector)
                                    :escape-html nil)
                       lines))
    (join lines (string #\Newline))))

(defun codec-encode (codec tables)
  (join (mapcar (if (eq codec :jsonl) #'encode-jsonl-table #'encode-table) tables) (string #\Newline)))

(defun encode-tables (tables) (codec-encode :rows tables))

(defun codec-name (codec) (if (eq codec :jsonl) "jsonl" "rows"))

(defun codec-spec-first-line (codec)
  (if (eq codec :jsonl)
      "A table is a header line {\"table\":\"name\",\"fields\":[\"field1\",\"field2\"]} followed by one line per row: a JSON array with one string per field, in header order, null for none."
      "A table is name[N]{field1,field2,...}: followed by exactly N rows, each on its own line indented two spaces, values separated by commas in header order with no space after a comma."))

(defun tables-instructions-for (codec)
  "TablesInstructionsFor."
  (if (or (null codec) (eq codec :rows))
      +tables-instructions+
      (cat +tables-summary-intro+ " in the " (codec-name codec) " format. " (trim-space (codec-spec-first-line codec)) +tables-body+)))

(defun absent-empty (cells)
  (loop for cell in cells for i from 0 collect (and (> i 0) (string= cell ""))))

;;; om.FormatRead (non-strict)

(defun raw-snippet-value (s)
  (if (contains-any s (format nil "`~C" #\Return)) (go-quote s) (cat "`" s "`")))

(defun format-read (fn &key where from through since until limit)
  "om.FormatRead for the options a summary's snippets carry."
  (let ((parts '()))
    (dolist (p where)
      (destructuring-bind (field . value) p
        (if (string= field "kind")
            (push-end (cat "Kind(" (raw-snippet-value value) ")") parts)
            (push-end (cat "Where(" (raw-snippet-value field) ", " (raw-snippet-value value) ")") parts))))
    (when from (push-end (cat "From(" (raw-snippet-value from) ")") parts))
    (when through (push-end (cat "Through(" (raw-snippet-value through) ")") parts))
    (when since (push-end (cat "Since(" (raw-snippet-value since) ")") parts))
    (when until (push-end (cat "Until(" (raw-snippet-value until) ")") parts))
    (when limit (push-end (cat "Limit(" (itoa limit) ")") parts))
    (cat "om." fn "(" (if parts (cat "om." (join parts ".")) "") ")")))

;;; Items and state

(defstruct item
  role index obs ref (text "") (group "") (which "") (note-group "")
  (anchor-id "") (anchor -1) anchored (from "") (to "") (status "") (links "")
  (extra "") (value "") (value-kind "") (count 0) (last -1)
  (cost 0) cells (line ""))

(defun item-source (it) (if (item-anchored it) (item-anchor-id it) ""))

(defun item-id (it)
  (case (item-role it)
    (:reflection (refl-id (item-ref it)))
    ((:request :intent) "")
    (t (obs-id (item-obs it)))))

(defstruct layout (tables t) (codec :rows) (intro "") (empty (make-set)) (notice t))

(defstruct sstate
  observations reflections folded branch request-in intent-in aliases reflection-dates layout
  items request intent positions canonical (budget 0)
  (superseded (make-set)) (by-id (make-set)) (records (make-set))
  (partners (make-set)) (superseded-by (make-set)))

(defun record-date (timestamp)
  (if (and (>= (length timestamp) 10) (iso-date-p (subseq timestamp 0 10))) (subseq timestamp 0 10) ""))

(defun record-anchor-day (timestamp)
  "selection's anchorTime: the day a timestamp starts with, nil for none."
  (let ((day (record-date timestamp)))
    (and (string/= day "") (millis-day (parse-time-string day)))))

(defun clip-runes (text n)
  (if (>= n (length text)) text (cat (trim-space (subseq text 0 n)) "…")))

;;; Layout

(defun fields-of (group)
  (cond ((string= group "request") +request-fields+)
        ((string= group "intent") +intent-fields+)
        ((string= group "reflections") +reflection-fields+)
        ((string= group "omitted") +omitted-fields+)
        (t +note-fields+)))

(defun list-group-p (group)
  (or (string= group "files") (string= group "commits") (string= group "values") (has-prefix group "values.")))

(defun value-group (kind) (cat "values." kind))

(defun extra-line (extra)
  (cond ((string= extra +extra-evidence+) +evidence-line+)
        ((string= extra +extra-values+) +values-line+)
        (t "")))

(defun extra-cost (extra) (estimate-tokens (extra-line extra)))

(defun value-cap (budget) (min +max-values-per-kind+ (max 1 (floor budget 200))))

(defun value-before-p (a b)
  "valueBefore: the values' rank order."
  (cond ((/= (item-count a) (item-count b)) (> (item-count a) (item-count b)))
        ((/= (item-last a) (item-last b)) (> (item-last a) (item-last b)))
        ((string/= (item-value a) (item-value b)) (string< (item-value a) (item-value b)))
        (t (< (item-index a) (item-index b)))))

(defun section-line (l group)
  (cond ((string= group "intent") +intent-line+)
        ((string= group "next") (if (layout-tables l) +next-line+ +next-lines-format+))
        (t "")))

(defun empty-cost (l group)
  (or (gethash group (layout-empty l))
      (setf (gethash group (layout-empty l))
            (estimate-tokens (codec-encode (layout-codec l) (list (make-rtable :name group :fields (fields-of group))))))))

(defun row-cost (l group cells)
  (if (not (layout-tables l))
      0
      (- (+ (estimate-tokens (codec-encode (layout-codec l)
                                           (list (make-rtable :name group :fields (fields-of group)
                                                              :rows (list cells) :absent (list (absent-empty cells))))))
            1)
         (empty-cost l group))))

(defun section-header-cost (l group)
  (cond ((list-group-p group) (1+ (estimate-tokens (cat +trailer-separator+ group ": "))))
        ((not (layout-tables l)) (+ (estimate-tokens (format nil "~%~%## ~A~%" group)) 2))
        (t (+ (empty-cost l group) 2))))

(defun header-cost (s group)
  (let ((l (sstate-layout s)))
    (+ (section-header-cost l group) (estimate-tokens (section-line l group)))))

(defun suffix (id) (if (string= id "") "" (cat " (" id ")")))

(defun price (s it)
  (let ((source (item-source it)) (l (sstate-layout s)))
    (case (item-role it)
      (:request (setf (item-cells it) (list (item-text it) source)
                      (item-line it) (cat "user: " (item-text it) (suffix source))))
      (:intent (setf (item-cells it) (list (item-which it) (item-text it) source)
                     (item-line it) (cat (item-which it) ": " (item-text it) (suffix source))))
      (:reflection
       (let* ((r (item-ref it))
              (dates (if (and (string/= (item-to it) "") (string/= (item-to it) (item-from it)))
                         (cat (item-from it) ".." (item-to it))
                         (item-from it))))
         (setf (item-cells it) (list (refl-id r) dates (refl-content r) source)
               (item-line it) (cat "[" (refl-id r) "]" (if (string/= dates "") (cat " (" dates ")") "")
                                   " " (refl-content r) (suffix source)))))
      (:value
       (setf (item-line it) (cat (item-value it) (if (> (item-count it) 1) (cat " ×" (itoa (item-count it))) "") (suffix source)))
       (setf (item-cost it) (1+ (estimate-tokens (cat ", " (item-line it)))))
       (return-from price))
      ((:file :commit)
       (let ((text (obs-content (item-obs it))))
         (when (eq (item-role it) :commit) (setf text (trim-prefix text "commit ")))
         (setf (item-line it) (cat text (suffix source)))
         (setf (item-cost it) (1+ (estimate-tokens (cat ", " (item-line it)))))
         (return-from price)))
      (t
       (let* ((o (item-obs it))
              (content (resolve-relative-dates (obs-content o) (record-anchor-day (obs-timestamp o))))
              (scope (canonical-scope (obs-scope o) (canonical-of (sstate-canonical s) (obs-subject o))))
              (replaced-by (join (gethash (obs-id o) (sstate-superseded-by s)) " "))
              (shown (if (string/= (item-links it) "") (cat content " " (item-links it)) content)))
         (setf (item-cells it) (list (obs-id o) (item-from it) (obs-kind o) (item-status it) scope shown replaced-by source))
         (let ((line (cat "[" (obs-id o) "]")))
           (dolist (part (list (item-from it) (obs-kind o) (item-status it)))
             (when (string/= part "") (setf line (cat line " " part))))
           (when (string/= scope "") (setf line (cat line " (" scope ")")))
           (setf line (cat line " " content))
           (when (string/= replaced-by "") (setf line (cat line " (superseded by " replaced-by ")")))
           (when (string/= (item-links it) "") (setf line (cat line " " (item-links it))))
           (setf (item-line it) (cat line (suffix source)))))))
    (setf (item-cost it) (if (layout-tables l)
                             (row-cost l (item-group it) (item-cells it))
                             (1+ (estimate-tokens (item-line it)))))))

(defun fixed-cost (&optional s)
  "What every summary pays: the instructions and the blank line after them (the native rows
codec's tables instructions without a state)."
  (1+ (estimate-tokens (if s (layout-intro (sstate-layout s)) +tables-instructions+))))

(defun new-layout (format codec)
  (let ((tables (not (equal format "lines"))))
    (make-layout :tables tables :codec (or codec :rows)
                 :intro (if tables (tables-instructions-for codec) +lines-instructions+))))

;;; newState

(defun make-state (observations folded branch budget &key request intent reflections (format "tables") codec
                                                         aliases reflection-dates)
  (let ((s (make-sstate :budget budget :observations observations :reflections reflections :folded folded :branch branch
                        :request-in request :intent-in intent :aliases aliases
                        :reflection-dates (or reflection-dates (make-set))
                        :layout (new-layout format codec) :positions (entry-index-by-id branch))))
    (maphash (lambda (id o) (setf (gethash id (sstate-records s)) o)) (folded-by-id folded))
    (dolist (o observations) (setf (gethash (obs-id o) (sstate-records s)) o))
    (let ((all (append (folded-observations folded)
                       (remove-if (lambda (o) (nth-value 1 (gethash (obs-id o) (folded-by-id folded)))) observations)))
          (items '()))
      (dolist (o all)
        (dolist (id (obs-supersedes o))
          (unless (string= id (obs-id o)) (setf (gethash id (sstate-superseded s)) t))))
      (loop for r in reflections for i from 0
            do (let ((it (make-item :role :reflection :index i :ref r :text (refl-content r) :group "reflections")))
                 (multiple-value-bind (id at ok) (reflection-anchor r (folded-by-id folded) (sstate-positions s))
                   (setf (item-anchor-id it) id (item-anchor it) at (item-anchored it) ok))
                 (multiple-value-bind (from to ranged) (cut (gethash (refl-id r) (sstate-reflection-dates s) "") " to ")
                   (setf (item-from it) from (item-to it) (if ranged to from)))
                 (push it items)
                 (setf (gethash (refl-id r) (sstate-by-id s)) it)))
      (setf (sstate-canonical s) (observation-subjects observations aliases))
      (loop for o in observations for i from 0
            do (let ((it (make-item :role :note :index i :obs o :text (obs-content o))))
                 (cond ((string= (obs-kind o) "file") (setf (item-role it) :file (item-group it) "files"))
                       ((string= (obs-kind o) "commit") (setf (item-role it) :commit (item-group it) "commits"))
                       ((string= (obs-kind o) "value")
                        (multiple-value-bind (kind value count ok) (value-parts o)
                          (if ok
                              (let ((last-id (car (last (obs-source-ids o)))))
                                (setf (item-role it) :value (item-group it) (value-group kind) (item-extra it) +extra-values+
                                      (item-value-kind it) kind (item-value it) value (item-count it) count
                                      (item-last it) (or (and last-id (gethash last-id (sstate-positions s))) -1)))
                              (setf (item-group it) "notes"))))
                       (t (let ((group (subject-table-name (canonical-subject (canonical-of (sstate-canonical s) (obs-subject o))))))
                            (setf (item-group it) (if (string= group "") "notes" group))
                            (when (string= (obs-kind o) "evidence") (setf (item-extra it) +extra-evidence+)))))
                 (multiple-value-bind (id at ok) (observation-anchor o (sstate-positions s))
                   (setf (item-anchor-id it) id (item-anchor it) at (item-anchored it) ok))
                 (setf (item-from it) (record-date (obs-timestamp o)) (item-to it) (item-from it))
                 (push it items)
                 (setf (gethash (obs-id o) (sstate-by-id s)) it)))
      (setf (sstate-items s) (nreverse items))
      (let ((retracted (retracted-ids all)))
        (dolist (it (sstate-items s))
          (when (eq (item-role it) :note)
            (setf (item-status it) (if (gethash (obs-id (item-obs it)) retracted) +status-retracted+ (obs-status (item-obs it)))))))
      (mark-next s)
      (link-items s)
      (dolist (o all)
        (dolist (id (obs-supersedes o))
          (when (and (string/= id (obs-id o)) (not (member-str (obs-id o) (gethash id (sstate-superseded-by s)))))
            (push-end (obs-id o) (gethash id (sstate-superseded-by s))))))
      (dolist (it (sstate-items s)) (price s it))
      (when (and request (string/= (trim-space (cdr request)) ""))
        (let ((it (make-item :role :request :text (one-line (truncate-record-content (cdr request))) :group "request"
                             :anchor-id (car request))))
          (setf (item-anchored it) (and (gethash (car request) (sstate-positions s)) t))
          (price s it)
          (setf (sstate-request s) it)))
      (dolist (r intent)
        (destructuring-bind (which id text) r
          (let ((text (one-line text)))
            (unless (string= text "")
              (let ((it (make-item :role :intent :which which :text (clip-runes text +history-clip-chars+)
                                   :group "intent" :anchor-id id)))
                (setf (item-anchored it) (and (gethash id (sstate-positions s)) t))
                (price s it)
                (push-end it (sstate-intent s))))))))
    s))

(defun mark-next (s)
  (let ((start -1) (ids '()))
    (when (sstate-request-in s) (push-end (car (sstate-request-in s)) ids))
    (dolist (r (sstate-intent-in s)) (push-end (second r) ids))
    (dolist (id ids)
      (let ((at (gethash id (sstate-positions s))))
        (when (and at (> at start)) (setf start at))))
    (when (< start 0) (return-from mark-next))
    (dolist (it (sstate-items s))
      (when (eq (item-role it) :note)
        (let ((o (item-obs it)))
          (unless (or (not (item-anchored it)) (< (item-anchor it) start)
                      (gethash (obs-id o) (sstate-superseded s)) (string= (item-status it) +status-retracted+))
            (let* ((attempt (string= (obs-kind o) "attempt"))
                   (open (and attempt (member (obs-outcome o) '("open" "failed") :test #'string=))))
              (when (or open (and (not attempt) (string= (item-status it) +status-proposed+)))
                (setf (item-note-group it) (item-group it) (item-group it) "next")))))))))

(defun link-items (s)
  (flet ((join-partner (a b)
           (unless (member b (gethash (item-id a) (sstate-partners s)) :test #'eq)
             (push-end b (gethash (item-id a) (sstate-partners s))))))
    (dolist (it (sstate-items s))
      (when (eq (item-role it) :note)
        (let ((o (item-obs it)) (parts '()) (was '()))
          (dolist (id (obs-supersedes o))
            (when (and (string/= id (obs-id o)) (not (member-str id was))) (push-end id was)))
          (when was (push-end (cat "(was " (join was " ") ")") parts))
          (flet ((pointer (label ids live)
                   (let ((shown '()))
                     (dolist (id ids)
                       (let ((other (gethash id (sstate-by-id s))))
                         (when (and other (not (eq other it)) (not (eq (item-role other) :reflection)) (funcall live other))
                           (join-partner it other)
                           (join-partner other it)
                           (push-end id shown))))
                     (when shown (push-end (cat "(" label " " (join shown " ") ")") parts)))))
            (pointer "may update" (obs-possibly-updates o)
                     (lambda (other) (not (gethash (obs-id (item-obs other)) (sstate-superseded s)))))
            (pointer "conflicts" (obs-conflicts o) (lambda (other) (declare (ignore other)) t)))
          (setf (item-links it) (join parts " ")))))
    (maphash (lambda (id items)
               (setf (gethash id (sstate-partners s)) (stable-sort (copy-list items) #'< :key #'item-index)))
             (sstate-partners s))))

;;; A choice

(defstruct sel
  (on (make-hash-table :test 'eq))
  (order '())
  (opened (make-set))
  (dropped (make-set))
  (used 0)
  (carried (make-array 0 :element-type 'character :adjustable t :fill-pointer 0))
  (request-text "")
  (intent-text (make-set)))

(defun on-p (p it) (gethash it (sel-on p)))

(defun shown-count (s p) (count-if (lambda (it) (on-p p it)) (sstate-items s)))

(defun sel-count (p) (loop for v being the hash-values of (sel-on p) count v))

(defun unselect (s p it)
  (setf (gethash it (sel-on p)) nil)
  (when (eq (item-role it) :request) (setf (sel-request-text p) ""))
  (when (eq (item-role it) :intent) (remhash (item-which it) (sel-intent-text p)))
  (decf (sel-used p) (item-cost it))
  (decf (gethash (item-group it) (sel-opened p)))
  (when (= (gethash (item-group it) (sel-opened p)) 0)
    (decf (sel-used p) (header-cost s (item-group it)))
    (remhash (item-group it) (sel-opened p)))
  (when (string/= (item-extra it) "")
    (decf (gethash (item-extra it) (sel-opened p)))
    (when (= (gethash (item-extra it) (sel-opened p)) 0)
      (decf (sel-used p) (extra-cost (item-extra it)))
      (remhash (item-extra it) (sel-opened p)))))

(defun pop-last (s p)
  (loop while (sel-order p)
        do (let ((it (pop (sel-order p))))
             (when (on-p p it)
               (unselect s p it)
               (return-from pop-last t))))
  nil)

(defun opened (p group) (gethash group (sel-opened p) 0))

(defun try-item (s p budget it)
  (when (or (on-p p it) (gethash (item-id it) (sel-dropped p)))
    (return-from try-item nil))
  (let ((cost (+ (item-cost it) (if (= (opened p (item-group it)) 0) (header-cost s (item-group it)) 0)
                 (if (and (string/= (item-extra it) "") (= (opened p (item-extra it)) 0)) (extra-cost (item-extra it)) 0))))
    (when (> (+ (sel-used p) cost) budget) (return-from try-item nil))
    (setf (gethash it (sel-on p)) t)
    (push it (sel-order p))
    (incf (sel-used p) cost)
    (setf (gethash (item-group it) (sel-opened p)) (1+ (opened p (item-group it))))
    (when (string/= (item-extra it) "")
      (setf (gethash (item-extra it) (sel-opened p)) (1+ (opened p (item-extra it)))))
    (loop for c across (item-text it) do (vector-push-extend c (sel-carried p)))
    (vector-push-extend #\Newline (sel-carried p))
    (let ((replaced (case (item-role it)
                      (:reflection (refl-supporting (item-ref it)))
                      ((:request :intent) '())
                      (t (obs-supersedes (item-obs it))))))
      (maphash (lambda (id v)
                 (declare (ignore v))
                 (unless (string= id (item-id it))
                   (setf (gethash id (sel-dropped p)) t)
                   (let ((other (gethash id (sstate-by-id s))))
                     (when (and other (not (eq (item-role other) :reflection)) (on-p p other))
                       (unselect s p other)))))
               (drop-closure replaced (sstate-observations s) (sstate-records s))))
    t))

(defun clipped-text (s it budget p)
  "clipped: the longest prefix of it's text whose item fits, or nil."
  (let* ((text (item-text it)) (room (- budget (sel-used p))))
    (flet ((fits (n)
             (let ((c (copy-item it)))
               (setf (item-text c) (clip-runes text n))
               (price s c)
               (<= (+ (item-cost c) (if (= (opened p (item-group c)) 0) (header-cost s (item-group c)) 0)) room))))
      (let ((lo 0) (hi (length text)))
        (loop while (< lo hi)
              do (let ((mid (floor (+ lo hi 1) 2)))
                   (if (fits mid) (setf lo mid) (setf hi (1- mid)))))
        (and (> lo 0) (clip-runes text lo))))))

(defun choose (s budget)
  (let ((p (make-sel)) (records (sstate-items s)))
    (labels ((try (it) (try-item s p budget it))
             (holds-no-value (it) (string= (item-status it) +status-retracted+))
             (newest-first (eligible roles)
               (dolist (it (reverse records))
                 (when (and (member (item-role it) roles) (funcall eligible it) (try it))
                   (dolist (other (gethash (item-id it) (sstate-partners s)))
                     (when (or (not (holds-no-value other)) (funcall eligible other))
                       (try other))))))
             (try-clipped (it on-text)
               (if (try it)
                   (funcall on-text (item-text it))
                   (let ((text (clipped-text s it budget p)))
                     (when text
                       (let ((c (copy-item it)))
                         (setf (item-text c) text)
                         (price s c)
                         (when (try c) (funcall on-text text))))))))
      (when (sstate-request s)
        (try-clipped (sstate-request s) (lambda (text) (setf (sel-request-text p) text))))
      (let ((observations '(:note :file :commit)))
        (newest-first (lambda (it) (string= (item-group it) "next")) '(:note))
        (let ((fallen '()))
          (dolist (it records)
            (when (and (string= (item-group it) "next") (not (on-p p it)))
              (setf (item-group it) (item-note-group it))
              (price s it)
              (push it fallen)))
          (newest-first (lambda (it) (member it fallen :test #'eq)) '(:note)))
        (newest-first (lambda (it) (and (user-priority-p (item-obs it)) (not (gethash (obs-id (item-obs it)) (sstate-superseded s)))
                                        (string/= (item-status it) +status-retracted+)))
                      observations)
        (newest-first (lambda (it) (let ((o (item-obs it)))
                                     (and (string= (obs-kind o) "attempt") (member (obs-outcome o) '("open" "failed") :test #'string=)
                                          (not (gethash (obs-id o) (sstate-superseded s))))))
                      observations)
        (dolist (it (reverse (sstate-intent s)))
          (let ((which (item-which it)))
            (try-clipped it (lambda (text) (setf (gethash which (sel-intent-text p)) text)))))
        (newest-first (lambda (it) (and (string/= (item-status it) +status-retracted+)
                                        (missing-specifics (item-text it) (list (coerce (sel-carried p) 'simple-string)))))
                      observations)
        ;; 4b. The values tool output printed, ranked, a rank at a time across the kinds.
        (let ((ranked (ranked-values s)))
          (dotimes (rank (value-cap (sstate-budget s)))
            (dolist (kind +value-kinds+)
              (let ((items (gethash kind ranked)))
                (when (and (< rank (length items))
                           (not (gethash (obs-id (item-obs (nth rank items))) (sstate-superseded s))))
                  (try (nth rank items)))))))
        (newest-first (lambda (it) (declare (ignore it)) t) '(:reflection))
        (newest-first (lambda (it) (declare (ignore it)) t) observations))
      p)))

(defun ranked-values (s)
  "rankedValues: the value items by kind (a table of lists), each in rank order."
  (let ((out (make-set)))
    (dolist (it (sstate-items s))
      (when (eq (item-role it) :value) (push-end it (gethash (item-value-kind it) out))))
    (maphash (lambda (k items) (setf (gethash k out) (stable-sort (copy-list items) #'value-before-p))) out)
    out))

(defun settle-drops (s p)
  (let ((shown '()) (reflections '()))
    (dolist (it (sstate-items s))
      (when (on-p p it)
        (if (eq (item-role it) :reflection) (push-end (item-ref it) reflections) (push-end (item-obs it) shown))))
    (setf (sel-dropped p) (make-set))
    (dolist (id (drops (sstate-observations s) shown (sstate-records s) reflections))
      (setf (gethash id (sel-dropped p)) t))))

;;; Omissions

(defstruct span-row (from "") (to "") (count 0) (spans 0) (first-id "") (last-id "") (query "")
  source (first-at 0) (last-at 0) positional in-range)

(defstruct tail (n 0) (all 0) (queries '()))

(defstruct omission (rows '()) (tails (make-set)) (records 0) (spans 0) (folded 0))

(defun omission-snippets (o)
  (mapcar #'redact-secrets
   (append (mapcar #'span-row-query (omission-rows o))
          (loop for group in '("files" "commits" "values")
                for tl = (gethash group (omission-tails o))
                when tl append (tail-queries tl)))))

(defun sorted-anchors (items)
  (sort (loop for it in items when (item-anchored it) collect (item-anchor it)) #'<))

(defun count-in (anchors lo hi)
  (count-if (lambda (a) (<= lo a hi)) anchors))

(defun snippet (source n &key from through since until)
  "snippet: the read with its bounds, limited when n rows exceed the default."
  (format-read (first source) :where (second source) :from from :through through :since since :until until
                              :limit (and (> n +default-limit+) (min n +max-limit+))))

(defun date-range (items)
  (let ((from "") (to ""))
    (dolist (it items (values from to))
      (when (and (string/= (item-from it) "") (or (string= from "") (string< (item-from it) from)))
        (setf from (item-from it)))
      (when (string> (item-to it) to) (setf to (item-to it))))))

(defun positional-query (source first last n) (snippet source n :from first :through last))

(defun positional-row (source run in-range)
  (let* ((first (first run)) (last (car (last run)))
         (r (make-span-row :count (length run) :spans 1 :first-id (item-anchor-id first) :last-id (item-anchor-id last)
                           :source source :first-at (item-anchor first) :last-at (item-anchor last)
                           :positional t :in-range in-range)))
    (multiple-value-bind (from to) (date-range run) (setf (span-row-from r) from (span-row-to r) to))
    (setf (span-row-query r) (positional-query source (span-row-first-id r) (span-row-last-id r)
                                               (funcall in-range (span-row-first-at r) (span-row-last-at r))))
    r))

(defun spans (source all left anchors breaks)
  (when (null left) (return-from spans nil))
  (let* ((order (stable-sort (remove-if-not #'item-anchored all)
                             (lambda (a b) (if (/= (item-anchor a) (item-anchor b))
                                               (< (item-anchor a) (item-anchor b))
                                               (< (item-index a) (item-index b))))))
         (is-left (make-hash-table :test 'eq))
         (in-range (lambda (lo hi) (count-in anchors lo hi)))
         (rows '()) (run '()))
    (dolist (it left) (setf (gethash it is-left) t))
    (flet ((flush () (when run (push (positional-row source (reverse run) in-range) rows) (setf run '()))))
      (dolist (it order)
        (cond ((funcall breaks it) (flush))
              ((not (gethash it is-left)))
              (t (when (and run (> (funcall in-range (item-anchor (car (last run))) (item-anchor it)) +max-limit+))
                   (flush))
                 (push it run))))
      (flush))
    (nreverse rows)))

(defun off-path-row (source left all)
  (let ((r (make-span-row :count (length left) :spans 1 :source source)))
    (multiple-value-bind (from to) (date-range left) (setf (span-row-from r) from (span-row-to r) to))
    (let ((id (item-anchor-id (first left)))) (when (string/= id "") (setf (span-row-first-id r) (cat id +off-path-label+))))
    (let ((id (item-anchor-id (car (last left))))) (when (string/= id "") (setf (span-row-last-id r) (cat id +off-path-label+))))
    (if (and (every (lambda (it) (string/= (item-from it) "")) left) (string/= (span-row-from r) ""))
        (let ((n (count-if (lambda (it) (and (string/= (item-from it) "") (string>= (item-to it) (span-row-from r))
                                             (string<= (item-from it) (span-row-to r))))
                           all)))
          (setf (span-row-query r) (snippet source n :since (span-row-from r) :until (span-row-to r))))
        (setf (span-row-query r) (snippet source (length all))))
    r))

(defun list-queries (base all p)
  (if (<= (length all) +max-limit+)
      (list (snippet base (length all)))
      (let ((left (remove-if (lambda (it) (or (on-p p it) (not (item-anchored it)))) all))
            (off-left (remove-if (lambda (it) (or (on-p p it) (item-anchored it))) all)))
        (append (mapcar #'span-row-query (spans base all left (sorted-anchors all) (lambda (it) (declare (ignore it)) nil)))
                (when off-left (list (span-row-query (off-path-row base off-left all))))))))

(defun fold-rows (rows k)
  (when (or (< k 2) (< (length rows) 2)) (return-from fold-rows rows))
  (let ((k (min k (length rows))) (out '()) (joined nil))
    (dolist (r (subseq rows 0 k))
      (if (and joined (<= (funcall (span-row-in-range joined) (span-row-first-at joined) (span-row-last-at r)) +max-limit+))
          (progn
            (setf (span-row-last-id joined) (span-row-last-id r) (span-row-last-at joined) (span-row-last-at r))
            (incf (span-row-count joined) (span-row-count r))
            (incf (span-row-spans joined) (span-row-spans r))
            (when (and (string/= (span-row-from r) "") (or (string= (span-row-from joined) "") (string< (span-row-from r) (span-row-from joined))))
              (setf (span-row-from joined) (span-row-from r)))
            (when (string> (span-row-to r) (span-row-to joined)) (setf (span-row-to joined) (span-row-to r))))
          (progn
            (when joined (push joined out))
            (setf joined (copy-span-row r)))))
    (push joined out)
    (setf out (nreverse out))
    (dolist (r out)
      (when (> (span-row-spans r) 1)
        (setf (span-row-query r) (positional-query (span-row-source r) (span-row-first-id r) (span-row-last-id r)
                                                   (funcall (span-row-in-range r) (span-row-first-at r) (span-row-last-at r))))))
    (append out (nthcdr k rows))))

(defun plural (n noun) (if (= n 1) noun (cat noun "s")))

(defun omitted-notice (shown o)
  (if (= (omission-records o) 0)
      ""
      (let ((where '()))
        (when (> (omission-spans o) 0)
          (push-end (cat (itoa (omission-spans o)) " " (plural (omission-spans o) "span") " under omitted") where))
        (dolist (group '("files" "commits" "values"))
          (let ((tl (gethash group (omission-tails o))))
            (when tl (push-end (cat "the +" (itoa (tail-n tl)) " more of the " group " line") where))))
        (cat +omitted-notice-start+ (itoa shown) " of " (itoa (+ shown (omission-records o))) " records; "
             (itoa (omission-records o)) " left out, listed by " (join where " and ") ". " +omitted-advice+))))

(defun notice-cost (s shown o)
  (let ((notice (omitted-notice shown o)))
    (if (or (not (layout-notice (sstate-layout s))) (string= notice ""))
        0
        (1+ (estimate-tokens (cat (format nil "~%~%") notice))))))

(defun omitted-cells (r)
  (list (span-row-from r) (span-row-to r) (itoa (span-row-count r))
        (if (> (span-row-spans r) 1) (itoa (span-row-spans r)) "")
        (span-row-first-id r) (span-row-last-id r) (span-row-query r)))

(defun omitted-line (r)
  "omittedLine: a span row in the lines format."
  (let ((line (cond ((and (string/= (span-row-from r) "") (string/= (span-row-to r) "") (string/= (span-row-from r) (span-row-to r)))
                     (cat (span-row-from r) ".." (span-row-to r) ": "))
                    ((string/= (span-row-from r) "") (cat (span-row-from r) ": "))
                    (t ""))))
    (setf line (cat line (itoa (span-row-count r)) " " (plural (span-row-count r) "record")))
    (when (> (span-row-spans r) 1) (setf line (cat line " in " (itoa (span-row-spans r)) " spans")))
    (when (string/= (span-row-first-id r) "")
      (setf line (cat line ", " (span-row-first-id r)))
      (when (and (string/= (span-row-last-id r) "") (string/= (span-row-last-id r) (span-row-first-id r)))
        (setf line (cat line " to " (span-row-last-id r)))))
    (cat line ": " (span-row-query r))))

(defun omitted-cost (s rows)
  (if (null rows)
      0
      (let ((l (sstate-layout s)))
        (+ (header-cost s "omitted")
           (loop for r in rows
                 sum (if (layout-tables l)
                         (row-cost l "omitted" (omitted-cells r))
                         (1+ (estimate-tokens (omitted-line r)))))))))

(defun tail-text (tl)
  (cat "+" (itoa (tail-n tl)) " more, all " (itoa (tail-all tl)) ": " (join (tail-queries tl) "; ")))

(defun tail-cost (s group tl opened)
  (+ (1+ (estimate-tokens (cat ", " (tail-text tl)))) (if opened 0 (header-cost s group))))

(defparameter +read-notes+ (list "Notes" nil))
(defparameter +read-reflections+ (list "Reflections" nil))
(defparameter +read-commits+ (list "Commits" nil))
(defparameter +read-files+ (list "Notes" (list (cons "kind" "file"))))
(defparameter +read-values+ (list "Notes" (list (cons "kind" "value"))))

(defun fewest (n fits)
  "The fewest k in [2, n] that fits, by halving: (values k ok)."
  (if (or (< n 2) (not (funcall fits n)))
      (values 0 nil)
      (let ((lo 2) (hi n))
        (loop while (< lo hi)
              do (let ((mid (floor (+ lo hi) 2)))
                   (if (funcall fits mid) (setf hi mid) (setf lo (1+ mid)))))
        (values lo t))))

(defun omissions (s p room)
  "omissions: (values omission need ok)."
  (let ((notes '()) (reflections '()) (off-notes '()) (off-reflections '())
        (note-anchors '()) (reflection-anchors '())
        (files 0) (commits 0) (file-items '()) (commit-items '()) (values-left 0) (value-items '()))
    (dolist (it (sstate-items s))
      (unless (gethash (item-id it) (sel-dropped p))
        (block one
          (case (item-role it)
            (:reflection
             (push-end it reflection-anchors)
             (unless (on-p p it)
               (if (item-anchored it) (push-end it reflections) (push-end it off-reflections)))
             (return-from one))
            (:file (push-end it file-items) (unless (on-p p it) (incf files)))
            (:commit (push-end it commit-items) (unless (on-p p it) (incf commits)))
            (:value (push-end it value-items) (unless (on-p p it) (incf values-left))))
          (push-end it note-anchors)
          (when (and (eq (item-role it) :note) (not (on-p p it)))
            (if (item-anchored it) (push-end it notes) (push-end it off-notes))))))
    (let ((o (make-omission)))
      (when (> files 0)
        (setf (gethash "files" (omission-tails o))
              (make-tail :n files :all (length file-items) :queries (list-queries +read-files+ file-items p)))
        (incf (omission-records o) files))
      (when (> commits 0)
        (setf (gethash "commits" (omission-tails o))
              (make-tail :n commits :all (length commit-items) :queries (list-queries +read-commits+ commit-items p)))
        (incf (omission-records o) commits))
      (when (> values-left 0)
        (setf (gethash "values" (omission-tails o))
              (make-tail :n values-left :all (length value-items) :queries (list-queries +read-values+ value-items p)))
        (incf (omission-records o) values-left))
      (let* ((breaks (lambda (it) (and (on-p p it) (member (item-role it) '(:note :reflection)))))
             (note-rows (spans +read-notes+ note-anchors notes (sorted-anchors note-anchors) breaks))
             (reflection-rows (spans +read-reflections+ reflection-anchors reflections (sorted-anchors reflection-anchors) breaks))
             (off-rows (append (when off-reflections (list (off-path-row +read-reflections+ off-reflections reflection-anchors)))
                               (when off-notes (list (off-path-row +read-notes+ off-notes note-anchors))))))
        (dolist (r (append note-rows reflection-rows off-rows))
          (incf (omission-records o) (span-row-count r))
          (incf (omission-spans o) (span-row-spans r)))
        (let ((tails-cost (notice-cost s (shown-count s p) o)))
          (maphash (lambda (group tl) (incf tails-cost (tail-cost s group tl (> (opened p group) 0))))
                   (omission-tails o))
          (labels ((layout (note-fold reflection-fold)
                     (append (fold-rows reflection-rows reflection-fold) (fold-rows note-rows note-fold) off-rows))
                   (try (rows)
                     (let ((cost (+ tails-cost (omitted-cost s rows)))
                           (out (copy-omission o)))
                       (setf (omission-rows out) rows
                             (omission-folded out) (count-if (lambda (r) (> (span-row-spans r) 1)) rows))
                       (values out cost (<= cost room))))
                   (fits (rows) (nth-value 2 (try rows))))
            (multiple-value-bind (out cost ok) (try (layout 0 0))
              (declare (ignore cost))
              (when ok (return-from omissions (values out 0 t))))
            (let ((n (length note-rows)) (m (length reflection-rows)))
              (multiple-value-bind (k ok) (fewest n (lambda (k) (fits (layout k 0))))
                (when ok (return-from omissions (values (try (layout k 0)) 0 t))))
              (multiple-value-bind (k ok) (fewest m (lambda (k) (fits (layout n k))))
                (when ok (return-from omissions (values (try (layout n k)) 0 t))))
              (try (layout n m)))))))))

;;; Rendering

(defun render-text (s p o)
  (let ((l (sstate-layout s)) (blocks '()) (by-group (make-set)) (newest (make-set)) (groups '()))
    (when (string/= (sel-request-text p) "")
      (let ((it (copy-item (sstate-request s))))
        (setf (item-text it) (sel-request-text p))
        (price s it)
        (push-end (cons "request" (list it)) blocks)))
    (let ((intent '()))
      (dolist (from (sstate-intent s))
        (let ((text (gethash (item-which from) (sel-intent-text p))))
          (when text
            (let ((it (copy-item from)))
              (setf (item-text it) text)
              (price s it)
              (push-end it intent)))))
      (when intent (push-end (cons "intent" intent) blocks)))
    (dolist (it (sstate-items s))
      (when (on-p p it)
        (unless (gethash (item-group it) by-group) (push-end (item-group it) groups))
        (push-end it (gethash (item-group it) by-group))
        (setf (gethash (item-group it) newest) (item-index it))))
    (when (gethash "next" by-group) (push-end (cons "next" (gethash "next" by-group)) blocks))
    (when (gethash "reflections" by-group) (push-end (cons "reflections" (gethash "reflections" by-group)) blocks))
    (let ((subjects (sort (remove-if (lambda (g) (or (member g '("reflections" "notes" "next") :test #'string=) (list-group-p g)))
                                     groups)
                          (lambda (a b) (if (/= (gethash a newest) (gethash b newest))
                                            (< (gethash a newest) (gethash b newest))
                                            (string< a b))))))
      (dolist (group subjects) (push-end (cons group (gethash group by-group)) blocks)))
    (when (gethash "notes" by-group) (push-end (cons "notes" (gethash "notes" by-group)) blocks))
    (let ((lists '()))
      (dolist (group (append '("files" "commits") (mapcar #'value-group +value-kinds+) '("values")))
        (let* ((items (gethash group by-group))
               (items (if (has-prefix group "values.") (stable-sort (copy-list items) #'value-before-p) items))
               (parts (mapcar #'item-line items))
               (tl (gethash group (omission-tails o))))
          (when tl (push-end (tail-text tl) parts))
          (when parts (push-end (cat group ": " (join parts ", ")) lists))))
      (let ((text (layout-intro l))
            (lines (apply #'cat (mapcar (lambda (b) (section-line l (car b))) blocks))))
        (dolist (extra (list +extra-evidence+ +extra-values+))
          (when (or (> (opened p extra) 0)
                    (and (string= extra +extra-values+) (let ((tl (gethash "values" (omission-tails o)))) (and tl (> (tail-n tl) 0)))))
            (setf lines (cat lines (extra-line extra)))))
        (when (string/= lines "")
          (let ((at (search +request-line+ text)))
            (when at
              (setf text (cat (subseq text 0 at) +request-line+ lines (subseq text (+ at (length +request-line+))))))))
        (let ((notice (omitted-notice (shown-count s p) o)))
          (when (and (layout-notice l) (string/= notice "")) (setf text (cat text (format nil "~%~%") notice))))
        (if (layout-tables l)
            (let ((out '()))
              (dolist (b blocks)
                (let ((tb (make-rtable :name (car b) :fields (fields-of (car b)))))
                  (dolist (it (cdr b))
                    (push-end (item-cells it) (rtable-rows tb))
                    (push-end (absent-empty (item-cells it)) (rtable-absent tb)))
                  (push-end tb out)))
              (when (omission-rows o)
                (let ((tb (make-rtable :name "omitted" :fields +omitted-fields+)))
                  (dolist (r (omission-rows o))
                    (let ((cells (omitted-cells r)))
                      (push-end cells (rtable-rows tb))
                      (push-end (absent-empty cells) (rtable-absent tb))))
                  (push-end tb out)))
              (when out (setf text (cat text (format nil "~%~%") (codec-encode (layout-codec l) out)))))
            (progn
              (dolist (b blocks)
                (setf text (cat text (format nil "~%~%## ~A~%" (car b)) (join (mapcar #'item-line (cdr b)) (string #\Newline)))))
              (when (omission-rows o)
                (setf text (cat text (format nil "~%~%## omitted~%") (join (mapcar #'omitted-line (omission-rows o)) (string #\Newline)))))))
        (when lists (setf text (cat text +trailer-separator+ (join lists (string #\Newline)))))
        text))))

(defstruct summary (text "") (observations '()) (reflections '()) (request nil)
  (omitted 0) (spans 0) (folded 0) (snippets '()))

(defun summary-result (s text pick omitted)
  (make-summary :text text
                :observations (loop for it in (sstate-items s)
                                    when (and (on-p pick it) (not (eq (item-role it) :reflection))) collect (item-obs it))
                :reflections (loop for it in (sstate-items s)
                                   when (and (on-p pick it) (eq (item-role it) :reflection)) collect (item-ref it))
                :request (string/= (sel-request-text pick) "")
                :omitted (omission-records omitted) :spans (omission-spans omitted)
                :folded (omission-folded omitted) :snippets (omission-snippets omitted)))

(defun render-summary (observations folded branch budget &rest options
                       &key request intent reflections format codec aliases reflection-dates)
  "selection.Render. request is (entry-id . text) or nil; intent a list of (which entry-id
text); format \"tables\" (the default) or \"lines\"; codec :rows (the default) or :jsonl;
reflection-dates a table of ReflectionDateRanges."
  (declare (ignore request intent format codec aliases reflection-dates))
  (when (or (<= budget 0) (and (null observations) (null reflections))) (return-from render-summary (make-summary)))
  (let* ((s (apply #'make-state observations folded branch budget options))
         (fixed (fixed-cost s)) (reserve 0) (pick nil) (omitted nil))
    (loop repeat (+ (length (sstate-items s)) 2)
          do (setf pick (choose s (- budget fixed reserve)))
             (settle-drops s pick)
             (multiple-value-bind (o need ok) (omissions s pick (- budget fixed (sel-used pick)))
               (setf omitted o)
               (when (or ok (<= need reserve) (> need (- budget fixed))) (return))
               (setf reserve need)))
    (loop
      ;; Secrets are redacted in what is shown (contracts/secrets.md).
      (let ((text (redact-secrets (render-text s pick omitted))))
        (when (and (<= (estimate-tokens text) budget) (> (sel-count pick) 0))
          (return (summary-result s text pick omitted)))
        (unless (pop-last s pick) (return (make-summary)))
        (settle-drops s pick)
        (setf omitted (omissions s pick (- budget fixed (sel-used pick))))))))

;;; OmittedSpans

(defstruct spans-result (lines '()) (snippets '()) (records 0))

(defun omitted-spans (observations reflections listed folded branch room)
  "OmittedSpans: (values spans need ok); listed is a set of the record ids shown elsewhere."
  (let* ((s (make-state observations folded branch 0 :reflections reflections :format "lines"
                                                      :reflection-dates (reflection-date-ranges (folded-reflections folded)
                                                                                                (folded-by-id folded))))
         (p (make-sel)))
    (setf (layout-notice (sstate-layout s)) nil)
    (dolist (it (sstate-items s))
      (when (gethash (item-id it) listed) (setf (gethash it (sel-on p)) t)))
    (multiple-value-bind (o need ok) (omissions s p room)
      (if (not ok)
          (values (make-spans-result) need nil)
          (let ((lines (mapcar #'omitted-line (omission-rows o))))
            (dolist (group '("files" "commits" "values"))
              (let ((tl (gethash group (omission-tails o))))
                (when tl (push-end (cat group ": " (tail-text tl)) lines))))
            (values (make-spans-result :lines lines :snippets (omission-snippets o) :records (omission-records o)) 0 t))))))
