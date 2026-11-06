;;;; Level 0's notes of memory tool calls (memory/extract/work.go note and addNote) and the
;;;; detectors' reading of old memory calls (memory/rules/detect.go MemoryAsks, legacyAsk), with
;;;; omtool.DetailsFrom's decoding: a code call is read from its result's details (its writes,
;;;; recalled and searches), a call written before the code tool parses its query with the frozen
;;;; recognizer (legacyquery.lisp), and a retired defined tool's call its stored query with the
;;;; call's input substituted. extract.lisp calls x-memory-note for every memory tool call, so the
;;;; extraction no longer refuses one (docs/lisp-port-status.md).

(in-package "OM.LEGACY")

(defun strings-or-null-p (v)
  (or (eq v :null) (and (vectorp v) (not (stringp v)) (every (lambda (x) (or (stringp x) (eq x :null))) v))))

(defun details-from (v)
  "omtool.DetailsFrom over decoded JSON: v when it is an object holding code and outcome whose
members decode into omtool.Details, else nil. (Go's decoder also matches member names without
regard to case; only the exact names are read here.)"
  (flet ((of (key test) (multiple-value-bind (x present) (gethash key v) (or (not present) (eq x :null) (funcall test x)))))
    (and (hash-table-p v)
         (nth-value 1 (gethash "code" v)) (nth-value 1 (gethash "outcome" v))
         (of "code" #'stringp) (of "outcome" #'stringp)
         (every (lambda (k) (of k #'strings-or-null-p)) '("strings" "hits" "recalled" "searches" "kept" "refused"))
         (every (lambda (k) (of k (lambda (x) (member x '(:true :false))))) '("empty" "manual" "legacy"))
         (of "writes" (lambda (w) (and (vectorp w) (not (stringp w))
                                       (every (lambda (e) (or (eq e :null)
                                                              (and (hash-table-p e)
                                                                   (every (lambda (k) (multiple-value-bind (x present) (gethash k e)
                                                                                        (or (not present) (eq x :null) (stringp x))))
                                                                          '("op" "name" "type" "description")))))
                                              w))))
         v)))

(defun jstr (v key) (let ((x (and (hash-table-p v) (gethash key v)))) (if (stringp x) x "")))

(defun jstrings (v key)
  (let ((x (and (hash-table-p v) (gethash key v))))
    (if (and (vectorp x) (not (stringp x))) (loop for e across x collect (if (stringp e) e "")) '())))

(defun details-writes (d)
  "Details.Writes as (op name description) lists."
  (let ((w (gethash "writes" d)))
    (if (and (vectorp w) (not (stringp w)))
        (loop for e across w collect (list (jstr e "op") (jstr e "name") (jstr e "description")))
        '())))

;;; extract/work.go note

(defun add-note (x i result name description)
  "addNote: the note a save or edit of name wrote, superseding the earlier one under it."
  (let ((description (trim-space description)))
    (when (or (string= name "") (string= description "")) (return-from add-note))
    (let* ((earlier (gethash name (om.engine::x-notes x)))
           (o (make-obs :content (one-line description) :kind "note" :origin "model" :target name
                        :subject (subject-in-text description (om.engine::x-subjects x) (folded-aliases (om.engine::x-ledger x))))))
      (setf (obs-supersedes o) (append (obs-supersedes o) earlier))
      (let ((r (om.engine::x-add x o i (om.engine::tresult-index result))))
        (setf (obs-id (om.engine::rec-obs r)) (om.engine::record-id (om.engine::rec-obs r)))
        (dolist (id earlier) (setf (gethash id (om.engine::x-superseded x)) t))
        (setf (gethash name (om.engine::x-notes x)) (list (obs-id (om.engine::rec-obs r))))))))

(defun om.engine::x-memory-note (x i call result)
  "extraction.note: a save or edit that landed is a note the session model wrote."
  (unless (om.engine::tresult-present result) (return-from om.engine::x-memory-note))
  (let* ((args (om.engine::tcall-args call))
         (code (and (hash-table-p args) (gethash "code" args))))
    (if (not (stringp code))
        (unless (om.engine::tresult-failed result)
          (let ((q (try-parse (jstr args "query"))))
            (when (and q (member (lquery-write q) '("save" "edit") :test #'string=))
              (add-note x i result (or (lq-arg q "name") "") (or (lq-arg q "description") "")))))
        (let ((d (details-from (om.engine::tresult-details result))))
          (when d
            (loop for (op name description) in (details-writes d)
                  when (member op '("save" "edit") :test #'string=)
                    do (add-note x i result name description)))))))

;;; rules/detect.go MemoryAsks

(defun om.layer::memory-asks (e defined results)
  "MemoryAsks: a list of (recalled . searches), one per memory call of the assistant entry that
asked for anything. defined maps a retired defined tool's name to its stored query (an alist)."
  (unless (om.layer::role-p e "assistant") (return-from om.layer::memory-asks '()))
  (let ((blocks (jget (om.layer::msg-of e) "content")) (out '()))
    (when (and (vectorp blocks) (not (stringp blocks)))
      (loop for b across blocks
            when (and (hash-table-p b) (equal (jget b "type") "toolCall"))
              do (let* ((name (jget b "name")) (args (jget b "arguments")) (args (if (hash-table-p args) args nil))
                        (query ""))
                   (cond ((equal name "memory")
                          (if (and args (stringp (gethash "code" args)))
                              (let ((d (details-from (gethash (str (jget b "id")) results))))
                                (let ((recalled (and d (jstrings d "recalled"))) (searches (and d (jstrings d "searches"))))
                                  (when (or recalled searches) (push (cons recalled searches) out)))
                                (setf query nil))
                              (setf query (jstr args "query"))))
                         ((and (stringp name) (assoc name defined :test #'string=))
                          (setf query (substitute-input (cdr (assoc name defined :test #'string=)) (jstr args "input")))))
                   (when query
                     (multiple-value-bind (recalled searches ok) (legacy-ask query)
                       (when ok (push (cons recalled searches) out)))))))
    (nreverse out)))
