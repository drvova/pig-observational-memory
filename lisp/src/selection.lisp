;;;; memory/selection: level 0's summary (selection.go, layout.go, omit.go) in the tables format
;;;; with the native rows codec (tables/rows_encode.go), and the om snippet formatter it prints
;;;; (om/snippet.go FormatRead). Not ported: reflections (only level 1 and v1 write them; one in
;;;; the input signals an error), the lines format and the other codecs.

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

(defparameter +request-line+
  (cat "- request: the opening request of the turn this compaction cut through; the turn continues after this summary." (string #\Newline)))
(defparameter +intent-line+
  (cat "- intent: what this session is for, in the user's words: first is the session's opening request, latest the latest request this summary covers; a cut one ends with … and its source reads it whole." (string #\Newline)))
(defparameter +next-line+
  (cat "- next: the next steps of the current work: plans still standing (status proposed) and attempts still open or last failed, made since the latest request; they are not repeated in the notes tables." (string #\Newline)))
(defparameter +trailer-separator+ (format nil "~%~%---~%~%"))
(defparameter +omitted-advice+
  (cat "Something missing from this summary may still be in memory: before saying it was not mentioned or is unknown, run the om snippet that lists what was left out or "
       +snippet-history-search+ "."))
(defparameter +omitted-notice-start+ "Shown here: ")

(defparameter +request-fields+ '("content" "source"))
(defparameter +intent-fields+ '("which" "content" "source"))
(defparameter +note-fields+ '("id" "when" "kind" "status" "scope" "content" "superseded_by" "source"))
(defparameter +omitted-fields+ '("from" "to" "count" "spans" "first_id" "last_id" "query"))

;;; tables: the rows codec

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

(defun encode-table (tb)
  (with-output-to-string (out)
    (format out "~A[~D]{~A}:" (rtable-name tb) (length (rtable-rows tb)) (join (rtable-fields tb) ","))
    (loop for row in (rtable-rows tb) for absent in (rtable-absent tb)
          do (write-string (format nil "~%  ") out)
             (loop for c from 0 below (length (rtable-fields tb))
                   do (when (> c 0) (write-char #\, out))
                      (cond ((and (< c (length row)) (not (nth c absent))) (write-string (quote-value (nth c row)) out))
                            ((= (length (rtable-fields tb)) 1) (write-string "\"\"" out)))))))

(defun encode-tables (tables)
  (join (mapcar #'encode-table tables) (string #\Newline)))

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
  role index obs (text "") (group "") (which "") (note-group "")
  (anchor-id "") (anchor -1) anchored (from "") (to "") (status "") (links "")
  (cost 0) cells (line ""))

(defun item-source (it) (if (item-anchored it) (item-anchor-id it) ""))

(defun item-id (it)
  (if (member (item-role it) '(:request :intent)) "" (obs-id (item-obs it))))

(defstruct sstate
  observations folded branch request-in intent-in budget
  items request intent positions
  (superseded (make-set)) (by-id (make-set)) (records (make-set))
  (partners (make-set)) (superseded-by (make-set))
  (empty-costs (make-set)))

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
        ((string= group "omitted") +omitted-fields+)
        (t +note-fields+)))

(defun list-group-p (group) (or (string= group "files") (string= group "commits")))

(defun section-line (group)
  (cond ((string= group "intent") +intent-line+)
        ((string= group "next") +next-line+)
        (t "")))

(defun empty-cost (s group)
  (or (gethash group (sstate-empty-costs s))
      (setf (gethash group (sstate-empty-costs s))
            (estimate-tokens (encode-table (make-rtable :name group :fields (fields-of group)))))))

(defun row-cost (s group cells)
  (- (+ (estimate-tokens (encode-table (make-rtable :name group :fields (fields-of group)
                                                    :rows (list cells) :absent (list (absent-empty cells)))))
        1)
     (empty-cost s group)))

(defun header-cost (s group)
  (+ (if (list-group-p group)
         (1+ (estimate-tokens (cat +trailer-separator+ group ": ")))
         (+ (empty-cost s group) 2))
     (estimate-tokens (section-line group))))

(defun suffix (id) (if (string= id "") "" (cat " (" id ")")))

(defun price (s it)
  (let ((source (item-source it)))
    (case (item-role it)
      (:request (setf (item-cells it) (list (item-text it) source)))
      (:intent (setf (item-cells it) (list (item-which it) (item-text it) source)))
      ((:file :commit)
       (let ((text (obs-content (item-obs it))))
         (when (eq (item-role it) :commit) (setf text (trim-prefix text "commit ")))
         (setf (item-line it) (cat text (suffix source)))
         (setf (item-cost it) (1+ (estimate-tokens (cat ", " (item-line it)))))
         (return-from price)))
      (t
       (let* ((o (item-obs it))
              (content (resolve-relative-dates (obs-content o) (record-anchor-day (obs-timestamp o))))
              (scope (multiple-value-bind (subject extra) (canonical-subject (obs-subject o))
                       (declare (ignore subject))
                       (canonical-scope (obs-scope o) extra)))
              (replaced-by (join (gethash (obs-id o) (sstate-superseded-by s)) " "))
              (shown (if (string/= (item-links it) "") (cat content " " (item-links it)) content)))
         (setf (item-cells it) (list (obs-id o) (item-from it) (obs-kind o) (item-status it) scope shown replaced-by source)))))
    (setf (item-cost it) (row-cost s (item-group it) (item-cells it)))))

(defun fixed-cost () (1+ (estimate-tokens +tables-instructions+)))

;;; newState

(defun make-state (observations folded branch budget &key request intent)
  (when (folded-has-reflections-p folded) (error "selection: reflections are not ported"))
  (let ((s (make-sstate :observations observations :folded folded :branch branch :budget budget
                        :request-in request :intent-in intent :positions (entry-index-by-id branch))))
    (maphash (lambda (id o) (setf (gethash id (sstate-records s)) o)) (folded-by-id folded))
    (dolist (o observations) (setf (gethash (obs-id o) (sstate-records s)) o))
    (let ((all (append (folded-observations folded)
                       (remove-if (lambda (o) (gethash (obs-id o) (folded-by-id folded))) observations))))
      (dolist (o all)
        (dolist (id (obs-supersedes o))
          (unless (string= id (obs-id o)) (setf (gethash id (sstate-superseded s)) t))))
      (let ((items '()))
        (loop for o in observations for i from 0
              do (let ((it (make-item :role :note :index i :obs o :text (obs-content o))))
                   (cond ((string= (obs-kind o) "file") (setf (item-role it) :file (item-group it) "files"))
                         ((string= (obs-kind o) "commit") (setf (item-role it) :commit (item-group it) "commits"))
                         (t (let ((group (subject-table-name (canonical-subject (obs-subject o)))))
                              (setf (item-group it) (if (string= group "") "notes" group)))))
                   (multiple-value-bind (id at ok) (observation-anchor o (sstate-positions s))
                     (setf (item-anchor-id it) id (item-anchor it) at (item-anchored it) ok))
                   (setf (item-from it) (record-date (obs-timestamp o)) (item-to it) (item-from it))
                   (push it items)
                   (setf (gethash (obs-id o) (sstate-by-id s)) it)))
        (setf (sstate-items s) (nreverse items)))
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

(defun folded-has-reflections-p (folded) (declare (ignore folded)) nil)

(defun mark-next (s)
  (let ((start -1) (ids '()))
    (when (sstate-request-in s) (push-end (car (sstate-request-in s)) ids))
    (dolist (r (sstate-intent-in s)) (push-end (second r) ids))
    (dolist (id ids)
      (let ((at (gethash id (sstate-positions s))))
        (when (and at (> at start)) (setf start at))))
    (when (< start 0) (return-from mark-next))
    (dolist (it (sstate-items s))
      (let ((o (item-obs it)))
        (unless (or (not (eq (item-role it) :note)) (not (item-anchored it)) (< (item-anchor it) start)
                    (gethash (obs-id o) (sstate-superseded s)) (string= (item-status it) +status-retracted+))
          (let* ((attempt (string= (obs-kind o) "attempt"))
                 (open (and attempt (member (obs-outcome o) '("open" "failed") :test #'string=))))
            (when (or open (and (not attempt) (string= (item-status it) +status-proposed+)))
              (setf (item-note-group it) (item-group it) (item-group it) "next"))))))))

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
                         (when (and other (not (eq other it)) (funcall live other))
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
    (remhash (item-group it) (sel-opened p))))

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
  (let ((cost (+ (item-cost it) (if (= (opened p (item-group it)) 0) (header-cost s (item-group it)) 0))))
    (when (> (+ (sel-used p) cost) budget) (return-from try-item nil))
    (setf (gethash it (sel-on p)) t)
    (push it (sel-order p))
    (incf (sel-used p) cost)
    (setf (gethash (item-group it) (sel-opened p)) (1+ (opened p (item-group it))))
    (loop for c across (item-text it) do (vector-push-extend c (sel-carried p)))
    (vector-push-extend #\Newline (sel-carried p))
    (let ((replaced (and (item-obs it) (obs-supersedes (item-obs it)))))
      (maphash (lambda (id v)
                 (declare (ignore v))
                 (unless (string= id (item-id it))
                   (setf (gethash id (sel-dropped p)) t)
                   (let ((other (gethash id (sstate-by-id s))))
                     (when (and other (on-p p other)) (unselect s p other)))))
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
        (newest-first (lambda (it) (declare (ignore it)) t) observations))
      p)))

(defun settle-drops (s p)
  (let ((shown (loop for it in (sstate-items s) when (on-p p it) collect (item-obs it))))
    (setf (sel-dropped p) (make-set))
    (dolist (id (drops (sstate-observations s) shown (sstate-records s)))
      (setf (gethash id (sel-dropped p)) t))))

;;; Omissions

(defstruct span-row (from "") (to "") (count 0) (spans 0) (first-id "") (last-id "") (query "")
  source (first-at 0) (last-at 0) positional in-range)

(defstruct tail (n 0) (all 0) (queries '()))

(defstruct omission (rows '()) (tails (make-set)) (records 0) (spans 0) (folded 0))

(defun omission-snippets (o)
  (append (mapcar #'span-row-query (omission-rows o))
          (loop for group in '("files" "commits")
                for tl = (gethash group (omission-tails o))
                when tl append (tail-queries tl))))

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

(defun list-queries (s base all p)
  (declare (ignore s))
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

(defun omitted-notice (shown o)
  (if (= (omission-records o) 0)
      ""
      (let ((where '()))
        (when (> (omission-spans o) 0)
          (push-end (cat (itoa (omission-spans o)) " " (plural (omission-spans o) "span") " under omitted") where))
        (dolist (group '("files" "commits"))
          (let ((tl (gethash group (omission-tails o))))
            (when tl (push-end (cat "the +" (itoa (tail-n tl)) " more of the " group " line") where))))
        (cat +omitted-notice-start+ (itoa shown) " of " (itoa (+ shown (omission-records o))) " records; "
             (itoa (omission-records o)) " left out, listed by " (join where " and ") ". " +omitted-advice+))))

(defun plural (n noun) (if (= n 1) noun (cat noun "s")))

(defun notice-cost (shown o)
  (let ((notice (omitted-notice shown o)))
    (if (string= notice "") 0 (1+ (estimate-tokens (cat (format nil "~%~%") notice))))))

(defun omitted-cells (r)
  (list (span-row-from r) (span-row-to r) (itoa (span-row-count r))
        (if (> (span-row-spans r) 1) (itoa (span-row-spans r)) "")
        (span-row-first-id r) (span-row-last-id r) (span-row-query r)))

(defun omitted-cost (s rows)
  (if (null rows)
      0
      (+ (header-cost s "omitted") (loop for r in rows sum (row-cost s "omitted" (omitted-cells r))))))

(defun tail-text (tl)
  (cat "+" (itoa (tail-n tl)) " more, all " (itoa (tail-all tl)) ": " (join (tail-queries tl) "; ")))

(defun tail-cost (s group tl opened)
  (+ (1+ (estimate-tokens (cat ", " (tail-text tl)))) (if opened 0 (header-cost s group))))

(defparameter +read-notes+ (list "Notes" nil))
(defparameter +read-commits+ (list "Commits" nil))
(defparameter +read-files+ (list "Notes" (list (cons "kind" "file"))))

(defun omissions (s p room)
  "omissions: (values omission need ok)."
  (let ((notes '()) (off-notes '()) (note-anchors '()) (files 0) (commits 0) (file-items '()) (commit-items '()))
    (dolist (it (sstate-items s))
      (unless (gethash (item-id it) (sel-dropped p))
        (case (item-role it)
          (:file (push-end it file-items) (unless (on-p p it) (incf files)))
          (:commit (push-end it commit-items) (unless (on-p p it) (incf commits))))
        (push-end it note-anchors)
        (when (and (eq (item-role it) :note) (not (on-p p it)))
          (if (item-anchored it) (push-end it notes) (push-end it off-notes)))))
    (let ((o (make-omission)))
      (when (> files 0)
        (setf (gethash "files" (omission-tails o))
              (make-tail :n files :all (length file-items) :queries (list-queries s +read-files+ file-items p)))
        (incf (omission-records o) files))
      (when (> commits 0)
        (setf (gethash "commits" (omission-tails o))
              (make-tail :n commits :all (length commit-items) :queries (list-queries s +read-commits+ commit-items p)))
        (incf (omission-records o) commits))
      (let* ((all-notes (sorted-anchors note-anchors))
             (breaks (lambda (it) (and (on-p p it) (eq (item-role it) :note))))
             (note-rows (spans +read-notes+ note-anchors notes all-notes breaks))
             (off-rows (when off-notes (list (off-path-row +read-notes+ off-notes note-anchors)))))
        (dolist (r (append note-rows off-rows))
          (incf (omission-records o) (span-row-count r))
          (incf (omission-spans o) (span-row-spans r)))
        (let ((tails-cost (notice-cost (shown-count s p) o)))
          (maphash (lambda (group tl) (incf tails-cost (tail-cost s group tl (> (opened p group) 0))))
                   (omission-tails o))
          (labels ((layout (note-fold) (append (fold-rows note-rows note-fold) off-rows))
                   (try (rows)
                     (let ((cost (+ tails-cost (omitted-cost s rows)))
                           (out (copy-omission o)))
                       (setf (omission-rows out) rows
                             (omission-folded out) (count-if (lambda (r) (> (span-row-spans r) 1)) rows))
                       (values out cost (<= cost room))))
                   (fits (k) (nth-value 2 (try (layout k)))))
            (multiple-value-bind (out cost ok) (try (layout 0))
              (declare (ignore cost))
              (when ok (return-from omissions (values out 0 t))))
            (let ((n (length note-rows)))
              (when (and (>= n 2) (fits n))
                (let ((lo 2) (hi n))
                  (loop while (< lo hi)
                        do (let ((mid (floor (+ lo hi) 2)))
                             (if (fits mid) (setf hi mid) (setf lo (1+ mid)))))
                  (return-from omissions (values (try (layout lo)) 0 t)))))
            (try (layout (length note-rows)))))))))

;;; Rendering

(defun render-text (s p o)
  (let ((blocks '()) (by-group (make-set)) (groups '()))
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
        (push-end it (gethash (item-group it) by-group))))
    (when (gethash "next" by-group) (push-end (cons "next" (gethash "next" by-group)) blocks))
    (when (some (lambda (g) (not (member g '("reflections" "notes" "next" "files" "commits") :test #'string=))) groups)
      (error "selection: subject tables are not ported"))
    (when (gethash "notes" by-group) (push-end (cons "notes" (gethash "notes" by-group)) blocks))
    (let ((lists '()))
      (dolist (group '("files" "commits"))
        (let ((parts (mapcar #'item-line (gethash group by-group)))
              (tl (gethash group (omission-tails o))))
          (when tl (push-end (tail-text tl) parts))
          (when parts (push-end (cat group ": " (join parts ", ")) lists))))
      (let ((text +tables-instructions+)
            (lines (apply #'cat (mapcar (lambda (b) (section-line (car b))) blocks))))
        (when (string/= lines "")
          (let ((at (search +request-line+ text)))
            (setf text (cat (subseq text 0 at) +request-line+ lines (subseq text (+ at (length +request-line+)))))))
        (let ((notice (omitted-notice (shown-count s p) o)))
          (when (string/= notice "") (setf text (cat text (format nil "~%~%") notice))))
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
          (when out (setf text (cat text (format nil "~%~%") (encode-tables out))))
          (when lists (setf text (cat text +trailer-separator+ (join lists (string #\Newline)))))
          text)))))

(defstruct summary (text "") (observations '()) (omitted 0) (spans 0) (folded 0) (snippets '()))

(defun render-summary (observations folded branch budget &key request intent)
  "selection.Render in the tables format with the rows codec. request is (entry-id . text) or
nil; intent a list of (which entry-id text)."
  (when (or (<= budget 0) (null observations)) (return-from render-summary (make-summary)))
  (let* ((s (make-state observations folded branch budget :request request :intent intent))
         (fixed (fixed-cost)) (reserve 0) (pick nil) (omitted nil))
    (loop repeat (+ (length (sstate-items s)) 2)
          do (setf pick (choose s (- budget fixed reserve)))
             (settle-drops s pick)
             (multiple-value-bind (o need ok) (omissions s pick (- budget fixed (sel-used pick)))
               (setf omitted o)
               (when (or ok (<= need reserve) (> need (- budget fixed))) (return))
               (setf reserve need)))
    (loop
      (let ((text (render-text s pick omitted)))
        (when (and (<= (estimate-tokens text) budget) (> (sel-count pick) 0))
          (return (make-summary :text text
                                :observations (loop for it in (sstate-items s) when (on-p pick it) collect (item-obs it))
                                :omitted (omission-records omitted) :spans (omission-spans omitted)
                                :folded (omission-folded omitted) :snippets (omission-snippets omitted))))
        (unless (pop-last s pick) (return (make-summary)))
        (settle-drops s pick)
        (setf omitted (omissions s pick (- budget fixed (sel-used pick))))))))
