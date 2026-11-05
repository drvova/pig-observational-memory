;;;; om.lisp: a mock of the memory tool's om reads in Common Lisp, over memory-data.lisp (the
;;;; same rows the real tool returns for the trial memory, written by make_data.py). It mirrors
;;;; the Go surface's semantics that the trial's tasks touch: Search (every word, case-insensitive,
;;;; over every column, ranked partial match when no row has every word), From/Through by entry,
;;;; Since/Until by date, Where/Contains (case-insensitive), Kind, WithStatus, SortBy, Limit
;;;; (20 by default, at most 200, with Matched counting every match), Failed/Worked/Open, and the
;;;; list helpers (Len, IDs, Count, Group, Filter) that run on the rows held.

(defpackage :om
  (:use :cl)
  (:export #:notes #:history #:files #:commits #:attempts #:note-by-id #:subjects
           #:note #:note-id #:note-when #:note-kind #:note-subject #:note-content #:note-status
           #:note-event-date #:note-origin #:note-outcome #:note-tool #:note-target #:note-source
           #:note-source-role #:note-anchor #:note-relevance #:note-superseded-by #:note-supersedes
           #:entry #:entry-id #:entry-when #:entry-role #:entry-text
           #:rows #:matched #:showing #:next-page #:partial #:len #:ids #:count-by #:group-by
           #:filter-rows #:field))

(in-package :om)

(defstruct note id when kind subject content status event-date origin outcome tool target source
  source-role anchor relevance superseded-by supersedes)
(defstruct entry id when role text)
(defstruct page rows matched showing next-page partial)

(defun rows (l) (page-rows l))
(defun matched (l) (page-matched l))
(defun showing (l) (page-showing l))
(defun next-page (l) (page-next-page l))
(defun partial (l) (page-partial l))

(defvar *data*
  (with-open-file (in (merge-pathnames "memory-data.lisp" (or *load-truename* *default-pathname-defaults*)))
    (let ((*read-eval* nil))
      (loop for form = (read in nil) while form collect form))))

(defun section (name) (cdr (assoc name *data*)))

(defparameter *notes*
  (mapcar (lambda (p) (apply #'make-note :allow-other-keys t p)) (section :notes)))
(defparameter *history*
  (mapcar (lambda (p) (apply #'make-entry :allow-other-keys t p)) (section :history)))

(defun field (row name)
  "The value of a column by its listing name (\"kind\", \"when\", \"superseded_by\", ...), as a string."
  (let* ((key (substitute #\- #\_ (string-upcase name)))
         (prefix (if (note-p row) "NOTE-" "ENTRY-"))
         (fn (find-symbol (concatenate 'string prefix key) :om)))
    (if (and fn (fboundp fn))
        (let ((v (funcall fn row)))
          (if (listp v) (format nil "~{~a~^ ~}" v) (or v "")))
        "")))

(defun columns (row)
  "The listing columns, which Search reads and Where, Contains and SortBy accept."
  (if (note-p row)
      '("id" "when" "kind" "subject" "scope" "relevance" "content" "superseded_by" "raw_subject"
        "subject_scope" "status" "event_date" "source" "source_role" "source_time")
      '("id" "when" "role" "text")))

(defun check-column (rows name read)
  (when (and rows (not (member name (columns (first rows)) :test #'string=)))
    (error "om:~a: unknown field ~s in ~a: the fields are ~{~a~^, ~}" read name
           (if (note-p (first rows)) "notes" "history") (columns (first rows)))))

(defun words (s)
  (let (out (cur '()))
    (loop for c across s
          do (if (member c '(#\Space #\Tab #\Newline))
                 (when cur (push (coerce (nreverse cur) 'string) out) (setf cur '()))
                 (push c cur)))
    (when cur (push (coerce (nreverse cur) 'string) out))
    (nreverse out)))

(defun has (hay needle) (search (string-downcase needle) (string-downcase hay)))

(defun row-text (row) (format nil "~{~a~^ ~}" (mapcar (lambda (c) (field row c)) (columns row))))

(defun row-anchor (row) (if (note-p row) (note-anchor row) (entry-id row)))

(defun entry-pos (id)
  "An entry or record id's place in the conversation (its anchor's)."
  (let ((n (find id *notes* :key #'note-id :test #'string=)))
    (when n (setf id (note-anchor n)))
    id))

(defun note-or-entry-when (r) (if (note-p r) (note-when r) (entry-when r)))

(defun date-of (when) (subseq when 0 (min 10 (length when))))

(defun select (read rows &key search from through since until where contains kind subject status
                      sort-by desc (limit 20) failed worked open)
  (let ((rs (copy-list rows)) (partialp nil))
    (when where (check-column rows (first where) read))
    (when contains (check-column rows (first contains) read))
    (when sort-by (check-column rows sort-by read))
    (when from (let ((p (entry-pos from))) (setf rs (remove-if (lambda (r) (string< (row-anchor r) p)) rs))))
    (when through (let ((p (entry-pos through))) (setf rs (remove-if (lambda (r) (string> (row-anchor r) p)) rs))))
    (when since (setf rs (remove-if (lambda (r) (string< (date-of (note-or-entry-when r)) since)) rs)))
    (when until (setf rs (remove-if (lambda (r) (string> (date-of (note-or-entry-when r)) until)) rs)))
    (when where (setf rs (remove-if-not (lambda (r) (string-equal (field r (first where)) (second where))) rs)))
    (when contains (setf rs (remove-if-not (lambda (r) (has (field r (first contains)) (second contains))) rs)))
    (when kind (setf rs (remove-if-not (lambda (r) (string-equal (field r "kind") kind)) rs)))
    (when subject (setf rs (remove-if-not (lambda (r) (string-equal (field r "subject") subject)) rs)))
    (when status (setf rs (remove-if-not (lambda (r) (string-equal (field r "status") status)) rs)))
    (when failed (setf rs (remove-if-not (lambda (r) (string= (field r "outcome") "failed")) rs)))
    (when worked (setf rs (remove-if-not (lambda (r) (string= (field r "outcome") "worked")) rs)))
    (when open (setf rs (remove-if-not (lambda (r) (string= (field r "outcome") "")) rs)))
    (when search
      (let* ((ws (words search))
             (all (remove-if-not (lambda (r) (every (lambda (w) (has (row-text r) w)) ws)) rs)))
        (if (or all (null ws))
            (setf rs all)
            (let ((scored (loop for r in rs
                                for n = (count-if (lambda (w) (has (row-text r) w)) ws)
                                when (plusp n) collect (cons n r))))
              (setf partialp t
                    rs (mapcar #'cdr (stable-sort scored #'> :key #'car)))))))
    (when sort-by
      (setf rs (stable-sort rs (if desc #'string> #'string<) :key (lambda (r) (field r sort-by)))))
    (let* ((limit (max 1 (min 200 limit)))
           (held (subseq rs 0 (min limit (length rs))))
           (cut (nthcdr limit rs)))
      (make-page :rows held :matched (length rs) :showing (length held) :partial partialp
                 :next-page (when cut (format nil "(om:~a :~a ~s :limit ~d)" read (if (string= read "history") "through" "from") (row-anchor (first cut)) limit))))))

(defun notes (&rest opts) "The active records, oldest first." (apply #'select "notes" *notes* opts))
(defun history (&rest opts) "User, assistant and tool text, newest first." (apply #'select "history" *history* opts))
(defun by-ids (ids) (mapcar (lambda (id) (find id *notes* :key #'note-id :test #'string=)) ids))
(defun files (&rest opts) "The file records: the newest state per path." (apply #'select "files" (by-ids (first (section :files))) opts))
(defun commits (&rest opts) "The commit records." (apply #'select "commits" (by-ids (first (section :commits))) opts))
(defun attempts (&rest opts) "The attempt records." (apply #'select "attempts" (by-ids (first (section :attempts))) opts))
(defun note-by-id (id)
  "One record by id; signals an error when there is none."
  (or (find id *notes* :key #'note-id :test #'string=) (error "no record ~a" id)))
(defun subjects ()
  "An alist (subject . count) of the active records' non-empty subjects, sorted by subject."
  (count-by (make-page :rows (remove "" *notes* :key #'note-subject :test #'string=)) "subject"))

(defun len (l) (length (page-rows l)))
(defun ids (l) (mapcar (lambda (r) (field r "id")) (page-rows l)))
(defun count-by (l name)
  "An alist (value . count) over the rows held, sorted by value."
  (let ((h (make-hash-table :test #'equal)) out)
    (dolist (r (page-rows l)) (incf (gethash (field r name) h 0)))
    (maphash (lambda (k v) (push (cons k v) out)) h)
    (sort out #'string< :key #'car)))
(defun group-by (l name)
  "An alist (value . page) over the rows held, sorted by value."
  (let ((h (make-hash-table :test #'equal)) out)
    (dolist (r (page-rows l)) (push r (gethash (field r name) h)))
    (maphash (lambda (k v) (push (cons k (make-page :rows (reverse v) :matched (length v) :showing (length v))) out)) h)
    (sort out #'string< :key #'car)))
(defun filter-rows (l keep)
  "A page of the rows held that KEEP accepts."
  (let ((rs (remove-if-not keep (page-rows l))))
    (make-page :rows rs :matched (length rs) :showing (length rs))))

(defmethod print-object ((p page) s)
  (format s "#<page matched ~d, showing ~d~@[, next: ~a~]>" (page-matched p) (page-showing p) (page-next-page p)))
