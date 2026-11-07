;;;; OM.TOOL, the API model code calls (docs/lisp-core.md, "Packages"): the reads over the
;;;; call's branch (notes, history, recall, saved), the curated writes (curated.lisp, with
;;;; curated.Check on every write), and the model's own layer (define, forget, rollback).
;;;; Ported from memory/om (the API), memory/omhost (reads.go, query.go, view.go) and
;;;; memory/curated; the reads keep omhost's filters and ranking but read the spike's ledger
;;;; (no subjects, aliases or reflections yet) and approximate serialize's history and recall
;;;; texts (docs/lisp-port-tool-layer.md, "Gaps").

(in-package "OM.LAYER")

(define-condition tool-error (error)
  ((message :initarg :message :reader tool-error-message))
  (:report (lambda (c s) (write-string (tool-error-message c) s))))

(defun tool-fail (fmt &rest args)
  (error 'tool-error :message (apply #'format nil fmt args)))

(defparameter +default-limit+ 20)
(defparameter +max-limit+ 200)

(defstruct note
  (id "") (when "") (kind "") (subject "") (scope "") (relevance "") (content "") (status "")
  (event-date "") (origin "") (outcome "") (tool "") (target "") (source "") (anchor "")
  (superseded-by '())
  ;; A ranked search's status tag and the records merged into the result (om.Note.Tag, With).
  (tag "") (with '())
  ;; A project record's freshness against HEAD (om.Note.Fresh): "gone at <sha>" or "changed
  ;; since <sha>, unverified", "" when unchanged or not checked; shown after its content.
  (fresh ""))

(defstruct turn (id "") (when "") (role "") (text ""))

(defmacro export-readers (struct &rest slots)
  `(progn
     ,@(loop for slot in slots
             for public = (find-symbol (format nil "~A-~A" struct slot) "OM.TOOL")
             for private = (intern (format nil "~A-~A" struct slot) "OM.LAYER")
             collect `(defun ,public (x) (,private x)))
     (defun ,(find-symbol (format nil "~A-P" struct) "OM.TOOL") (x)
       (,(intern (format nil "~A-P" struct) "OM.LAYER") x))))

(export-readers note id when kind subject scope relevance content status event-date origin outcome tool target source anchor superseded-by tag with fresh)
(export-readers turn id when role text)

(defun om.tool:memory-name (m) (mem-name m))
(defun om.tool:memory-type (m) (mem-type m))
(defun om.tool:memory-description (m) (mem-description m))
(defun om.tool:memory-body (m) (mem-body m))
(defun om.tool:memory-p (m) (memory-p m))

(defun go-note-line (n)
  "om.Note.Line: the record as one summary line, ending with its anchor."
  (with-output-to-string (b)
    (when (string/= (note-kind n) "") (format b "[~A] " (note-kind n)))
    (when (string/= (note-subject n) "") (format b "~A: " (note-subject n)))
    (write-string (note-content n) b)
    (when (string/= (note-fresh n) "") (format b " (~A)" (note-fresh n)))
    (when (string/= (note-anchor n) "") (format b " ~A" (note-anchor n)))))

;;; The adaptable functions the reads use

(defun note-haystack (n)
  (string-downcase
   (om::join (list (note-id n) (note-when n) (note-kind n) (note-subject n) (note-scope n)
                   (note-relevance n) (note-content n) (note-status n) (note-event-date n)
                   (note-origin n) (note-outcome n) (note-tool n) (note-target n) (note-source n))
             (string #\Newline))))

(defun as-list (x) (if (listp x) x (list x)))

(defun search-words (searches)
  (let ((out '()))
    (dolist (s searches (nreverse out))
      (dolist (w (om::fields (string-downcase s)))
        (pushnew w out :test #'string=)))))

(defun word-score (hay words) (count-if (lambda (w) (search w hay)) words))

(defun permutation-check (result notes words)
  (declare (ignore words))
  (cond ((not (listp result)) "the result is not a list")
        ((/= (length result) (length notes)) "the result does not hold every candidate once")
        ((notevery (lambda (n) (= 1 (count n result))) notes) "the result does not hold every candidate once")
        (t nil)))

(define-adaptable om.engine::rank-notes (notes words) (:check #'permutation-check)
  "Orders the notes a search ranks when no note has every searched word: each holds at least
one, and they come in the search's order (om.engine::retrieval-search: most searched words first,
then its ranking). The default keeps that order, as omhost does. The kernel's check: the result
holds every candidate exactly once."
  (progn words (copy-list notes)))

(defun phrase-check (line note)
  (cond ((not (stringp line)) "the line is not a string")
        ((find #\Newline line) "the line is not one line")
        ((and (string/= (note-anchor note) "") (not (om::has-suffix line (note-anchor note))))
         "the line does not end with the record's anchor")
        ((om::missing-specifics (note-content note) (list line))
         (format nil "the line drops ~{~A~^, ~}" (om::missing-specifics (note-content note) (list line))))
        (t nil)))

(define-adaptable om.engine::phrase-note (note) (:check #'phrase-check)
  "How a record reads as one line in the memory tool's output. The default is om.Note.Line.
The kernel's check: one line, ending with the record's anchor, carrying every specific of its
content."
  (go-note-line note))

(defun om.tool:note-line (n) (om.engine::phrase-note n))

;;; Notes

(defun the-call () (or *call* (tool-fail "no memory tool call is in flight")))

(defun branch () (call-branch (the-call)))

(defun superseded-by-map (folded)
  "Each record id -> the ids of the records that supersede it, in fold order (om.Note.SupersededBy)."
  (let ((h (make-hash-table :test 'equal)))
    (dolist (o (om::folded-observations folded) h)
      (dolist (id (om::obs-supersedes o))
        (unless (or (string= id (om::obs-id o)) (member (om::obs-id o) (gethash id h) :test #'string=))
          (setf (gethash id h) (append (gethash id h) (list (om::obs-id o)))))))))

(defun note-of (o positions &optional superseded)
  (let ((day (om::record-anchor-day (om::obs-timestamp o))))
    (make-note :id (om::obs-id o)
               :when (if (string= (om::obs-timestamp o) "") "" (om::format-timestamp (om::obs-timestamp o)))
               :kind (om::obs-kind o) :subject (om::obs-subject o) :scope (om::obs-scope o)
               :relevance (om::obs-relevance o)
               :content (if day (om::resolve-relative-dates (om::obs-content o) day) (om::obs-content o))
               :status (om::obs-status o) :event-date (om::obs-event-date o) :origin (om::obs-origin o)
               :outcome (om::obs-outcome o) :tool (om::obs-tool o) :target (om::obs-target o)
               :source (or (first (om::obs-source-ids o)) "")
               :anchor (multiple-value-bind (id at ok) (om::observation-anchor o positions)
                         (declare (ignore at)) (if ok id ""))
               :superseded-by (and superseded (copy-list (gethash (om::obs-id o) superseded))))))

(defun limit-of (limit)
  (cond ((null limit) +default-limit+)
        ((or (not (integerp limit)) (< limit 1)) (tool-fail "Limit(~A): a limit is 1 to ~D" limit +max-limit+))
        (t (min limit +max-limit+))))

(defun day-of (when) (if (>= (length when) 10) (subseq when 0 10) ""))

(defun filter-notes (notes &key search kind status subject since until)
  (let ((needles (search-words (as-list search))))
    (remove-if-not
     (lambda (n)
       (and (every (lambda (w) (search w (note-haystack n))) needles)
            (or (null kind) (string-equal kind (note-kind n)))
            (or (null status) (string-equal status (note-status n)))
            (or (null subject) (string-equal subject (note-subject n)))
            (or (null since) (and (string/= (day-of (note-when n)) "") (string>= (day-of (note-when n)) since)))
            (or (null until) (and (string/= (day-of (note-when n)) "") (string<= (day-of (note-when n)) until)))))
     notes)))

(defun all-notes ()
  (let* ((entries (branch)) (positions (om::entry-index-by-id entries)) (folded (om::fold-ledger entries))
         (superseded (superseded-by-map folded)))
    (mapcar (lambda (o) (note-of o positions superseded)) (om::folded-active folded))))

(defun bound-position (id folded positions)
  "omhost's From/Through bound: an entry's branch position, else a record's own anchor's."
  (multiple-value-bind (at ok) (gethash id positions)
    (when ok (return-from bound-position at)))
  (let ((o (gethash id (om::folded-by-id folded))))
    (when o
      (multiple-value-bind (aid at ok) (om::observation-anchor o positions)
        (declare (ignore aid))
        (when ok (return-from bound-position at)))))
  (tool-fail "entry ~A is not on this branch: take entry ids from history or from a summary line on this branch" id))

(defun tag-filter-p (status)
  "A status option naming a search tag rather than a record status (retracted is both)."
  (and status (member status '("believed" "superseded" "contested" "held") :test #'string=)))

(defun om.tool:notes (&key search kind status subject since until from through limit)
  "The active folded records, oldest first, filtered: kind, status (a record status, or a tag:
believed, superseded, contested or held), subject, since and until (YYYY-MM-DD), from and through
(an entry or record id: the records anchored at or after, at or before its branch position).
search ranks what those filters leave (docs/protocol.md, \"Retrieval\"; om.engine::retrieval-search):
a record matches on an exact key (path, symbol, test, error line, version or SHA, byte for byte)
or when it holds every word, as a substring or through identifier parts; current records before
superseded or retracted ones, exact hits first, then by relevance, newest first; each result
carries its note-tag and the contiguous records of its episode or source entry it merged
(note-with). A search no record matches ranks the records holding any of its words, most first
(rank-notes). At most limit (1 to 200, 20 when not given) results."
  (let* ((n (limit-of limit))
         (entries (branch)) (positions (om::entry-index-by-id entries)) (folded (om::fold-ledger entries))
         (superseded (superseded-by-map folded))
         (retracted (om::retracted-ids (om::folded-observations folded)))
         (active (om::folded-active folded))
         (contested (om::contested active))
         (pairs (mapcar (lambda (o)
                          (let ((note (note-of o positions superseded)))
                            (setf (note-tag note)
                                  (om::tag-of o (and (note-superseded-by note) t) (gethash (om::obs-id o) retracted)
                                              (nth-value 1 (gethash (om::obs-id o) contested))))
                            (cons note o)))
                        active))
         (by-tag (tag-filter-p status))
         (kept-notes (filter-notes (mapcar #'car pairs) :kind kind :status (if by-tag nil status)
                                   :subject subject :since since :until until)))
    (when by-tag
      (setf kept-notes (remove-if-not (lambda (x) (string= (note-tag x) status)) kept-notes)))
    (when (or from through)
      (let* ((lo (and from (bound-position from folded positions)))
             (hi (and through (bound-position through folded positions))))
        (setf kept-notes (remove-if-not (lambda (x)
                                          (multiple-value-bind (pos ok) (gethash (note-anchor x) positions)
                                            (and ok (or (null lo) (>= pos lo)) (or (null hi) (<= pos hi)))))
                                        kept-notes))))
    (let ((searches (as-list search)))
      (if (null searches)
          (progn (dolist (x kept-notes) (setf (note-tag x) ""))
                 (with-freshness (subseq kept-notes 0 (min n (length kept-notes)))))
          (let* ((records (let ((h (make-hash-table :test 'eq)))
                            (dolist (p pairs h) (setf (gethash (car p) h) (cdr p)))))
                 (pool (map 'vector (lambda (x)
                                      (om::doc-of (gethash x records) (note-content x) (note-tag x)
                                                  :hay (note-haystack x)))
                            kept-notes))
                 (notes (coerce kept-notes 'vector)))
            (multiple-value-bind (results partial) (om::retrieval-search pool searches)
              (let ((heads (mapcar (lambda (r)
                                     (destructuring-bind (hit exact with) r
                                       (declare (ignore exact))
                                       (let ((head (copy-note (aref notes hit))))
                                         (setf (note-with head) (mapcar (lambda (i) (aref notes i)) with))
                                         head)))
                                   results)))
                (when partial
                  (setf heads (om.engine::rank-notes heads (search-words searches))))
                (mapcar (lambda (h)
                          (let ((h (first (with-freshness (list h)))))
                            (setf (note-with h) (with-freshness (note-with h)))
                            h))
                        (subseq heads 0 (min n (length heads)))))))))))

(defun call-fresh-texts (call)
  "The call's freshness verdict texts by record id, read from the host once, at the first notes
read that shows rows (omhost's Call.freshness); nil for none."
  (let ((f (call-freshness call)))
    (when (functionp f)
      (setf f (funcall f) (call-freshness call) f))
    f))

(defun with-freshness (notes)
  "The notes with each project record's freshness verdict in note-fresh (omhost's Note.Fresh,
which note-line prints after the content, as om.Note.Line does)."
  (let ((fresh (and notes (call-fresh-texts (the-call)))))
    (if (null fresh)
        notes
        (mapcar (lambda (n)
                  (let ((v (gethash (note-id n) fresh)))
                    (if v
                        (let ((c (copy-note n))) (setf (note-fresh c) v) c)
                        n)))
                notes))))

;;; The HEAD structure view (om.Head)

(defparameter +head-page+ 50 "omhost.HeadPage: om.Head's default page.")
(defparameter +max-head-page+ 500 "omhost.MaxHeadPage: om.Head's largest page.")

(defparameter +no-view+
  "this session is in no git repository the host reads (the HEAD structure view is unavailable)"
  "omhost's errNoView.")

(defstruct head-view
  "om.HeadView: a page of the session repository's files at HEAD, derived on demand by the host
and never stored."
  (repo-id "") (head "") (prefix "") (files '()) (offset 0) (total 0) (truncated nil) (next ""))

(export-readers head-view repo-id head prefix files offset total truncated next)

(defun head-view-string (v)
  "om.HeadView.String without its last newline (show adds it): a header line, then one path per
line, then the next page when there is one."
  (let ((head (head-view-head v)))
    (when (> (length head) 7) (setf head (subseq head 0 7)))
    (string-right-trim
     '(#\Newline)
     (with-output-to-string (b)
       (format b "HEAD ~A: ~D files" head (head-view-total v))
       (when (string/= (head-view-prefix v) "") (format b " under ~A" (head-view-prefix v)))
       (when (head-view-truncated v)
         (write-string " (the tree is larger than the host lists: a path missing here is unknown)" b))
       (when (head-view-files v)
         (format b ", showing ~D from ~D" (length (head-view-files v)) (head-view-offset v)))
       (terpri b)
       (dolist (f (head-view-files v)) (write-string f b) (terpri b))
       (when (string/= (head-view-next v) "") (format b "next: ~A~%" (head-view-next v)))))))

(defun om.tool:head (prefix &key limit offset)
  "Pages HEAD's files under prefix (om.Head): the repository's tree at HEAD is read on demand and
never stored; the page is in path order, limit (50 by default, at most 500) files from offset;
outside a repository it is an error."
  (unless (stringp prefix) (tool-fail "head takes a path prefix string"))
  (let ((structure (call-structure (the-call)))
        (limit (or limit 0)) (offset (or offset 0)))
    (unless structure (tool-fail "~A" +no-view+))
    (unless (and (integerp limit) (integerp offset) (>= limit 0) (>= offset 0))
      (tool-fail "limit and offset must not be negative"))
    (when (zerop limit) (setf limit +head-page+))
    (setf limit (min limit +max-head-page+))
    (let ((a (funcall structure (om::jobject "list" (om::jobject "prefix" prefix "offset" offset "limit" limit)))))
      (unless (hash-table-p a) (tool-fail "~A" +no-view+))
      (let* ((files (remove-if-not #'stringp (om::jlist-of (om::jget a "files"))))
             (total (let ((n (om::jget a "total"))) (if (realp n) (truncate n) 0)))
             (end (+ offset (length files))))
        (make-head-view :repo-id (om::str (om::jget a "repoId")) :head (om::str (om::jget a "head"))
                        :prefix prefix :files files :offset offset :total total
                        :truncated (eq (om::jget a "truncated") :true)
                        :next (if (and files (< end total))
                                  (format nil "om.Head(~A, om.Offset(~D), om.Limit(~D))" (om::go-quote prefix) end limit)
                                  ""))))))

(defun om.tool:note-by-id (id)
  "One record by id, dropped ones included."
  (let* ((entries (branch)) (folded (om::fold-ledger entries))
         (o (gethash id (om::folded-by-id folded))))
    (unless o (tool-fail "no record ~A on this branch" (q id)))
    (note-of o (om::entry-index-by-id entries) (superseded-by-map folded))))

;;; History

(defun current-turn-start (entries)
  (or (position-if (lambda (e) (and (string= (om::entry-type e) "message") (equal (om::entry-role e) "user")))
                   entries :from-end t)
      (length entries)))

(defun history-text (e)
  "An entry's role and text as history reads it (an approximation of serialize.HistoryText:
user text without markup, assistant text blocks, tool result text), or nil."
  (when (and (string= (om::entry-type e) "message") (om::entry-message e))
    (let* ((msg (om::entry-message e)) (role (om::jget msg "role"))
           (content (om::jget msg "content")))
      (multiple-value-bind (role text)
          (cond ((equal role "user") (values "user" (om::without-markup (om::text-blocks content))))
                ((equal role "assistant") (values "assistant" (om::text-blocks content)))
                ((equal role "toolResult") (values "tool" (om::text-blocks content)))
                (t (values nil nil)))
        (when (and role (string/= (om::trim-space text) ""))
          (values role text))))))

(defun om.tool:history (&key search limit)
  "User, assistant and tool text, newest first, without the current turn; search keeps the
rows holding every word."
  (let* ((n (limit-of limit))
         (entries (branch))
         (needles (search-words (as-list search)))
         (rows '()))
    (loop for i from 0 below (current-turn-start entries)
          for e = (aref entries i)
          do (multiple-value-bind (role text) (history-text e)
               (when role
                 (let ((row (make-turn :id (om::entry-id e)
                                       :when (if (string= (om::entry-timestamp e) "") "" (om::format-timestamp (om::entry-timestamp e)))
                                       :role role :text text)))
                   (when (every (lambda (w) (search w (string-downcase (om::join (list (turn-id row) (turn-when row) (turn-role row) (turn-text row)) (string #\Newline))))) needles)
                     (push row rows))))))
    (subseq rows 0 (min n (length rows)))))

(defun user-texts ()
  (loop for e across (branch)
        for (role text) = (multiple-value-list (history-text e))
        when (equal role "user") collect text))

(defun conversation-texts ()
  (loop for e across (branch)
        for (role text) = (multiple-value-list (history-text e))
        when (member role '("user" "assistant") :test #'equal) collect text))

;;; Recall

(defun page-of (text offset)
  "omhost.PageOf: (values page from to total ok)."
  (let ((total (length text)))
    (cond ((and (plusp offset) (>= offset total)) (values "" 0 0 total nil))
          ((and (zerop offset) (<= total +recall-page-chars+)) (values text 0 total total t))
          (t (let ((end (min total (+ offset +recall-page-chars+))))
               (when (< end total)
                 (loop for i from (1- end) above offset
                       when (char= (char text i) #\Newline) do (setf end (1+ i)) (return)))
               (values (subseq text offset end) offset end total t))))))

(defun recall-text (id)
  (let* ((entries (branch)) (folded (om::fold-ledger entries)))
    (when (om::memory-id-p id)
      (let ((o (gethash id (om::folded-by-id folded))))
        (when o
          (let ((n (note-of o (om::entry-index-by-id entries))))
            (return-from recall-text
              (with-output-to-string (out)
                (write-string (om.tool:note-line n) out)
                (dolist (src (om::obs-source-ids o))
                  (let ((e (find src entries :key #'om::entry-id :test #'string=)))
                    (when e
                      (multiple-value-bind (role text) (history-text e)
                        (when role (format out "~%~%[~A ~A] ~A" role src text))))))))))))
    (let ((e (find id entries :key #'om::entry-id :test #'string=)))
      (when e
        (multiple-value-bind (role text) (history-text e)
          (unless role
            (tool-fail "entry ~A is a ~A entry with no conversation text" id
                       (if (string= (om::entry-custom-type e) "") (om::entry-type e)
                           (format nil "~A ~A" (om::entry-type e) (om::entry-custom-type e)))))
          (return-from recall-text (format nil "[~A] ~A" role text)))))
    (let ((c (the-call)))
      (when (and (call-curated-dir c) (valid-name-p id))
        (handler-case
            (let ((m (get-as (call-curated-dir c) id (call-project c) (call-session c))))
              (append-usage (call-curated-dir c) id "view")
              (return-from recall-text (curated-describe m)))
          (not-found () nil)
          (curated-error (e) (tool-fail "~A" e)))))
    (tool-fail "nothing called ~A: no record id, entry id or saved name on this branch" (q id))))

(defun om.tool:recall (id &key (offset 0))
  "A record's sources, else an entry, else a saved memory by name, one page at offset."
  (let ((text (recall-text (if (symbolp id) (string-downcase (symbol-name id)) id))))
    (multiple-value-bind (page from to total ok) (page-of text offset)
      (declare (ignore from))
      (unless ok (tool-fail "offset ~D is past the end: ~A is ~D characters" offset id total))
      (if (< to total)
          (format nil "~A~%(characters ~D of ~D: (recall ~S :offset ~D) reads on)" page to total id to)
          page))))

;;; Saved and the curated writes

(defun om.tool:saved (&key search)
  "The curated memories this project and session see, ranked as the index ranks them; search
keeps the ones holding every word (case-insensitive, over name, type, description and body)."
  (let ((c (the-call)) (needles (search-words (as-list search))))
    (unless (call-curated-dir c) (tool-fail "curated memory is off"))
    (remove-if-not (lambda (m)
                     (let ((hay (string-downcase (om::join (list (mem-name m) (mem-type m) (mem-description m) (mem-body m))
                                                           (string #\Newline)))))
                       (every (lambda (w) (search w hay)) needles)))
                   (curated-rank (shown-as (call-curated-dir c) (list-as (call-curated-dir c) (call-project c) (call-session c)) (call-session c))
                                 (handler-case (curated-usage (call-curated-dir c)) (error () nil))))))

(defun write-call ()
  (let ((c (the-call)))
    (unless (call-curated-dir c) (tool-fail "curated memory is off"))
    (make-curated-call :dir (call-curated-dir c) :project (call-project c) :source (call-session c)
                       :evidence (lambda (quote) (quoted-in-p (conversation-texts) quote))
                       :user-evidence (lambda (quote) (quoted-in-p (user-texts) quote))
                       :source-of (lambda (quote) (quoted-source (tool-texts) quote)))))

(defun tool-texts ()
  "The tool results' texts on the branch, the memory tool's own left out (omhost.QuotedSource's
skip), so text the model printed through it is no source."
  (loop for e across (branch)
        for (role text) = (multiple-value-list (history-text e))
        when (and (equal role "tool")
                  (not (equal (om::jget (om::entry-message e) "toolName") "memory")))
          collect text))

(defun quoted-source (texts quote)
  "omhost.QuotedSource over texts: the source-digest of the first holding quote, \"\" for none."
  (let* ((needle (string-downcase (one-line quote)))
         (text (find-if (lambda (text) (search needle (string-downcase (one-line text)))) texts)))
    (if text (source-digest text) "")))

(defun run-write (op &rest pairs)
  (let ((args (make-hash-table :test 'equal)))
    (loop for (k v) on pairs by #'cddr
          when v do (setf (gethash k args) (if (stringp v) v (princ-to-string v))))
    (multiple-value-bind (text err) (curated-run (write-call) op args)
      (if err (tool-fail "~A" err) text))))

(defun om.tool:save (name type description &key body scope quoting)
  "Saves a curated memory, staged unless quoting gives the user's confirming words."
  (run-write "save" "name" name "type" type "description" description "body" body "scope" scope "quote" quoting))

(defun om.tool:edit (name &key type description body quoting)
  "Changes a curated memory for every session when quoting gives a user's words asking for it;
without them it changes the memory in this session only and stages the edit. It never pins one."
  (run-write "edit" "name" name "type" type "description" description "body" body "quote" quoting))

(defun om.tool:forget-memory (name &key quoting)
  "Forgets a curated memory for every session when quoting gives a user's words asking for it;
without them it hides the memory in this session only and stages the forget."
  (run-write "forget" "name" name "quote" quoting))

(defun om.tool:feedback (name outcome quote)
  "Records that a memory helped or harmed, quoting the conversation."
  (run-write "feedback" "name" name "outcome" outcome "quote" quote))

;;; The model's layer

(defmacro om.tool:define (name lambda-list &body body)
  "A top-level form of the memory tool: holds a definition of an adaptable function."
  (declare (ignore lambda-list body))
  `(tool-fail "(define ~(~A~) ...) is a top-level form of the memory tool" ',name))

(defun model-of-call () (let ((m (call-model (the-call)))) (if (string= m "") (tool-fail "no session model") m)))

(defun name-of (x) (if (symbolp x) (string-downcase (symbol-name x)) x))

(defun om.tool:layer ()
  "The session model's layer: each settled and held definition, and the generations."
  (let* ((model (model-of-call)) (l (load-layer model)))
    (append (mapcar (lambda (d) (format nil "settled ~A ~A" (ldef-name d) (ldef-digest d))) (loaded-settled l))
            (mapcar (lambda (d) (format nil "held ~A ~A" (ldef-name d) (ldef-digest d))) (loaded-held l))
            (mapcar (lambda (r) (format nil "refused ~A" r)) (loaded-reports l))
            (list (format nil "generation ~D" (layer-generations model))))))

(defun layer-gate (quoting what)
  "The quote gate on a layer change (layerWriter.gate): (values quote ok reason) over the curated
store's usage ledger, which counts the user's words; with curated memory off nothing is changed
or staged."
  (let ((c (write-call)) (args (make-hash-table :test 'equal)))
    (when quoting (setf (gethash "quote" args) (if (stringp quoting) quoting (princ-to-string quoting))))
    (multiple-value-bind (quote err)
        (handler-case (values (gate-quote args what) nil) (curated-error (e) (values nil (curated-error-message e))))
      (when err (tool-fail "~A" err))
      (multiple-value-bind (ok reason) (covers c quote)
        (values quote ok reason c)))))

(defun om.tool:layer-forget (name &key quoting)
  "Forgets the session model's definition of the adaptable function, the default standing now,
when quoting gives a user's words asking for it; without them it stages the forget and changes
nothing."
  (let ((model (model-of-call)) (name (name-of name)))
    (unless (layer-holds-p model name) (tool-fail "~A's layer holds no definition of ~A" model name))
    (multiple-value-bind (quote ok reason c) (layer-gate quoting "layer forget")
      (unless ok
        (record-layer-change (cc-dir c) "layer-forget-staged" (layer-key +kind-adaptable+ name) model (cc-source c) nil nil)
        (return-from om.tool:layer-forget
          (format nil "Staged the forget of ~A from ~A's layer: ~A, so the layer is unchanged for every session, this one included, and /om:status lists the staged forget. Forgetting it needs words copied verbatim from a user message asking for it, quoted with the forget." name model reason)))
      (let ((gen (handler-case (layer-forget model name) (refused (e) (tool-fail "~A" (refused-reason e))))))
        (record-layer-change (cc-dir c) "layer-forget" (layer-key +kind-adaptable+ name) model (cc-source c) quote nil)
        (format nil "Forgot ~A from ~A's layer (generation ~D); the default stands." name model gen)))))

(defun om.tool:layer-rollback (generation &key quoting)
  "Makes the session model's layer what it was at the generation (0: every default) when quoting
gives a user's words asking for it; without them it stages the rollback and changes nothing."
  (let ((model (model-of-call)))
    (handler-case (check-rollback-target model generation) (refused (e) (tool-fail "~A" (refused-reason e))))
    (multiple-value-bind (quote ok reason c) (layer-gate quoting "layer rollback")
      (unless ok
        (record-layer-change (cc-dir c) "layer-rollback-staged" +layer-rollback-key+ model (cc-source c) nil generation)
        (return-from om.tool:layer-rollback
          (format nil "Staged the rollback of ~A's layer to generation ~D: ~A, so the layer is unchanged for every session, this one included, and /om:status lists the staged rollback. Rolling it back needs words copied verbatim from a user message asking for it, quoted with the rollback." model generation reason)))
      (let ((gen (handler-case (layer-rollback model generation) (refused (e) (tool-fail "~A" (refused-reason e))))))
        (record-layer-change (cc-dir c) "layer-rollback" +layer-rollback-key+ model (cc-source c) quote generation)
        (format nil "Rolled ~A's layer back to generation ~D (generation ~D)." model generation gen)))))

(defun om.tool:adaptables ()
  "The adaptable functions a definition may replace, with what each does."
  (mapcar (lambda (name)
            (let ((a (gethash name *adaptables*)))
              (format nil "~A (~D ~A): ~A" name (adaptable-arity a)
                      (if (= 1 (adaptable-arity a)) "argument" "arguments") (adaptable-doc a))))
          (adaptable-names)))

(defun om.tool:show (x)
  "Prints x as the memory tool shows a value."
  (write-string (show-string x))
  (terpri)
  x)
