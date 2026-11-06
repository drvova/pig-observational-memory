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
  (superseded-by '()))

(defstruct turn (id "") (when "") (role "") (text ""))

(defmacro export-readers (struct &rest slots)
  `(progn
     ,@(loop for slot in slots
             for public = (find-symbol (format nil "~A-~A" struct slot) "OM.TOOL")
             for private = (intern (format nil "~A-~A" struct slot) "OM.LAYER")
             collect `(defun ,public (x) (,private x)))
     (defun ,(find-symbol (format nil "~A-P" struct) "OM.TOOL") (x)
       (,(intern (format nil "~A-P" struct) "OM.LAYER") x))))

(export-readers note id when kind subject scope relevance content status event-date origin outcome tool target source anchor superseded-by)
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
one. The default is omhost's rankBySearch: most distinct searched words first, ties in ledger
order. The kernel's check: the result holds every candidate exactly once."
  (stable-sort (copy-list notes) #'> :key (lambda (n) (word-score (note-haystack n) words))))

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

(defun om.tool:notes (&key search kind status subject since until from through limit)
  "The active folded records, oldest first, filtered: search (every word, case-insensitive,
over every field), kind, status, subject, since and until (YYYY-MM-DD), from and through (an
entry or record id: the records anchored at or after, at or before its branch position). A
search no record matches with every word ranks the records holding any of its words instead
(rank-notes). At most limit (1 to 200, 20 when not given)."
  (let* ((n (limit-of limit))
         (all (all-notes))
         (kept (filter-notes all :search search :kind kind :status status :subject subject :since since :until until)))
    (when (and (null kept) search)
      (let* ((words (search-words (as-list search)))
             (candidates (remove-if-not (lambda (x) (plusp (word-score (note-haystack x) words)))
                                        (filter-notes all :kind kind :status status :subject subject :since since :until until))))
        (when candidates (setf kept (om.engine::rank-notes candidates words)))))
    (when (or from through)
      (let* ((entries (branch)) (positions (om::entry-index-by-id entries)) (folded (om::fold-ledger entries))
             (lo (and from (bound-position from folded positions)))
             (hi (and through (bound-position through folded positions))))
        (setf kept (remove-if-not (lambda (x)
                                    (multiple-value-bind (pos ok) (gethash (note-anchor x) positions)
                                      (and ok (or (null lo) (>= pos lo)) (or (null hi) (<= pos hi)))))
                                  kept))))
    (subseq kept 0 (min n (length kept)))))

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
                   (curated-rank (remove-if-not (lambda (m) (seen-by-p m (call-session c)))
                                                (list-memories (call-curated-dir c) (call-project c)))
                                 (handler-case (curated-usage (call-curated-dir c)) (error () nil))))))

(defun write-call ()
  (let ((c (the-call)))
    (unless (call-curated-dir c) (tool-fail "curated memory is off"))
    (make-curated-call :dir (call-curated-dir c) :project (call-project c) :source (call-session c)
                       :evidence (lambda (quote) (quoted-in-p (conversation-texts) quote))
                       :user-evidence (lambda (quote) (quoted-in-p (user-texts) quote)))))

(defun run-write (op &rest pairs)
  (let ((args (make-hash-table :test 'equal)))
    (loop for (k v) on pairs by #'cddr
          when v do (setf (gethash k args) (if (stringp v) v (princ-to-string v))))
    (multiple-value-bind (text err) (curated-run (write-call) op args)
      (if err (tool-fail "~A" err) text))))

(defun om.tool:save (name type description &key body scope quoting)
  "Saves a curated memory, staged unless quoting gives the user's confirming words."
  (run-write "save" "name" name "type" type "description" description "body" body "scope" scope "quote" quoting))

(defun om.tool:edit (name &key type description body)
  "Changes a curated memory; it never pins one."
  (run-write "edit" "name" name "type" type "description" description "body" body))

(defun om.tool:forget-memory (name)
  "Retires a curated memory."
  (run-write "forget" "name" name))

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

(defun om.tool:layer-forget (name)
  "Forgets the session model's definition of the adaptable function: the default stands now."
  (let* ((model (model-of-call)) (gen (handler-case (layer-forget model (name-of name))
                                        (refused (e) (tool-fail "~A" (refused-reason e))))))
    (format nil "Forgot ~A from ~A's layer (generation ~D); the default stands." (name-of name) model gen)))

(defun om.tool:layer-rollback (generation)
  "Makes the session model's layer what it was at the generation (0: every default)."
  (let* ((model (model-of-call)) (gen (handler-case (layer-rollback model generation)
                                        (refused (e) (tool-fail "~A" (refused-reason e))))))
    (format nil "Rolled ~A's layer back to generation ~D (generation ~D)." model generation gen)))

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
